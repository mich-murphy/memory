"""Object stores: R2 (S3 API) and a local directory with the same key layout."""

from __future__ import annotations

from pathlib import Path
from typing import Protocol

from .config import Config


class Store(Protocol):
    def put(self, key: str, body: bytes, metadata: dict[str, str] | None = None) -> None: ...

    def exists(self, key: str) -> bool: ...

    def delete(self, key: str) -> None: ...


class LocalStore:
    def __init__(self, root: Path):
        self.root = root

    def put(self, key: str, body: bytes, metadata: dict[str, str] | None = None) -> None:
        path = self.root / key
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_name(path.name + ".tmp")
        tmp.write_bytes(body)
        tmp.replace(path)

    def exists(self, key: str) -> bool:
        return (self.root / key).exists()

    def delete(self, key: str) -> None:
        (self.root / key).unlink(missing_ok=True)


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


def open_store(cfg: Config) -> Store:
    if cfg.local_store:
        return LocalStore(cfg.local_store)
    return R2Store(cfg)
