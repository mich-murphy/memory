"""Object stores: R2 (S3 API) and a local directory with the same key layout."""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Protocol

from .config import Config


class Store(Protocol):
    def put(self, key: str, body: bytes, metadata: dict[str, str] | None = None) -> None: ...

    def put_file(self, key: str, path: Path, metadata: dict[str, str] | None = None) -> None: ...

    def copy(self, src: str, dst: str) -> bool:
        """Server-side copy; False if src doesn't exist."""
        ...

    def exists(self, key: str) -> bool: ...

    def delete(self, key: str) -> None: ...

    def get(self, key: str) -> bytes: ...

    def list(self, prefix: str) -> list[str]: ...


class LocalStore:
    def __init__(self, root: Path):
        self.root = root

    def put(self, key: str, body: bytes, metadata: dict[str, str] | None = None) -> None:
        path = self.root / key
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_name(path.name + ".tmp")
        tmp.write_bytes(body)
        tmp.replace(path)

    def put_file(self, key: str, path: Path, metadata: dict[str, str] | None = None) -> None:
        dest = self.root / key
        dest.parent.mkdir(parents=True, exist_ok=True)
        tmp = dest.with_name(dest.name + ".tmp")
        shutil.copyfile(path, tmp)
        tmp.replace(dest)

    def copy(self, src: str, dst: str) -> bool:
        if not (self.root / src).is_file():
            return False
        self.put_file(dst, self.root / src)
        return True

    def exists(self, key: str) -> bool:
        return (self.root / key).exists()

    def delete(self, key: str) -> None:
        (self.root / key).unlink(missing_ok=True)

    def get(self, key: str) -> bytes:
        return (self.root / key).read_bytes()

    def list(self, prefix: str) -> list[str]:
        base = self.root / prefix.rsplit("/", 1)[0] if "/" in prefix else self.root
        if not base.is_dir():
            return []
        keys = (p.relative_to(self.root).as_posix() for p in base.rglob("*") if p.is_file() and not p.name.endswith(".tmp"))
        return sorted(k for k in keys if k.startswith(prefix))


class R2Store:
    def __init__(self, cfg: Config):
        import boto3
        from botocore.config import Config as BotoConfig

        missing = cfg.missing_r2_settings()
        if missing:
            raise RuntimeError(f"missing R2 settings: {', '.join(missing)}")
        self.bucket = cfg.r2_bucket
        self.client = boto3.client(
            "s3",
            endpoint_url=cfg.r2_endpoint,
            aws_access_key_id=cfg.r2_access_key_id,
            aws_secret_access_key=cfg.r2_secret_access_key,
            region_name="auto",
            config=BotoConfig(retries={"max_attempts": 5, "mode": "standard"}),
        )

    def put(self, key: str, body: bytes, metadata: dict[str, str] | None = None) -> None:
        self.client.put_object(Bucket=self.bucket, Key=key, Body=body, Metadata=metadata or {})

    def put_file(self, key: str, path: Path, metadata: dict[str, str] | None = None) -> None:
        # upload_file switches to a multipart upload for large files, streaming from disk.
        self.client.upload_file(str(path), self.bucket, key, ExtraArgs={"Metadata": metadata or {}})

    def copy(self, src: str, dst: str) -> bool:
        from botocore.exceptions import ClientError

        try:
            self.client.copy_object(Bucket=self.bucket, Key=dst, CopySource={"Bucket": self.bucket, "Key": src})
            return True
        except ClientError as e:
            if e.response.get("Error", {}).get("Code") in ("404", "NoSuchKey", "NotFound"):
                return False
            raise

    def exists(self, key: str) -> bool:
        from botocore.exceptions import ClientError

        try:
            self.client.head_object(Bucket=self.bucket, Key=key)
            return True
        except ClientError as e:
            if e.response.get("Error", {}).get("Code") in ("404", "NoSuchKey", "NotFound"):
                return False
            raise

    def delete(self, key: str) -> None:
        self.client.delete_object(Bucket=self.bucket, Key=key)

    def get(self, key: str) -> bytes:
        return self.client.get_object(Bucket=self.bucket, Key=key)["Body"].read()

    def list(self, prefix: str) -> list[str]:
        keys = []
        for page in self.client.get_paginator("list_objects_v2").paginate(Bucket=self.bucket, Prefix=prefix):
            keys.extend(o["Key"] for o in page.get("Contents", []))
        return keys


def open_store(cfg: Config) -> Store:
    if cfg.local_store:
        return LocalStore(cfg.local_store)
    return R2Store(cfg)
