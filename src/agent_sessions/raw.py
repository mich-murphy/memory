"""Compressing raw objects, and reading a raw file back from its pieces.

A raw file is stored as a base object plus the append segments uploaded since it was last
folded (see docs/storage-layout.md). Each object is zstd and holds bytes [start, end) of the
file; the file is their concatenation in start order.
"""

from __future__ import annotations

import hashlib
import io
from typing import BinaryIO

import zstandard

LEVEL = 10


def compress(data: bytes) -> bytes:
    return zstandard.ZstdCompressor(level=LEVEL).compress(data)


def compress_stream(src: BinaryIO, dst: BinaryIO, length: int, chunk: int) -> str:
    """Compress the next `length` bytes of src into dst; return their sha256."""
    h = hashlib.sha256()
    with zstandard.ZstdCompressor(level=LEVEL).stream_writer(dst, closefd=False) as out:
        left = length
        while left > 0:
            block = src.read(min(chunk, left))
            if not block:
                break
            h.update(block)
            out.write(block)
            left -= len(block)
    return h.hexdigest()


def decompress(body: bytes) -> bytes:
    with zstandard.ZstdDecompressor().stream_reader(io.BytesIO(body), read_across_frames=True) as r:
        return r.read()


def segment_key(raw_key: str, start: int, end: int) -> str:
    return f"{raw_key}.d/{start:015d}-{end:015d}.zst"


def blob_key(sha: str) -> str:
    return f"blobs/{sha}.zst"


def join(get, pieces: list[dict]) -> bytes:
    """Rebuild a file from pieces as manifests list them. `get(key)` returns an object's body."""
    out = bytearray()
    for p in sorted(pieces, key=lambda p: p["start"]):
        if p["start"] != len(out):
            raise ValueError(f"gap or overlap at byte {len(out)}: next piece starts at {p['start']}")
        data = decompress(get(p["object"]))
        if len(data) != p["end"] - p["start"] or hashlib.sha256(data).hexdigest() != p["sha256"]:
            raise ValueError(f"{p['object']} does not match its sha256 or length")
        out += data
    return bytes(out)


def read_key(store, raw_key: str) -> bytes:
    """Rebuild a file from the bucket alone, without a manifest: the base object, then each
    segment that starts where the bytes so far end. Segments left behind by a fold that could
    not delete them start before that point and are skipped."""
    out = bytearray(decompress(store.get(raw_key)))
    for key in sorted(store.list(raw_key + ".d/")):
        start, end = (int(x) for x in key.rsplit("/", 1)[1].removesuffix(".zst").split("-"))
        if start == len(out):
            out += decompress(store.get(key))
    return bytes(out)
