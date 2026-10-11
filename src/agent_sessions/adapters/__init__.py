from __future__ import annotations

from pathlib import Path

from .base import Adapter, SessionFile
from .claude import ClaudeAdapter
from .codex import CodexAdapter
from .pi import PiAdapter

ADAPTER_CLASSES: dict[str, type[Adapter]] = {"claude": ClaudeAdapter, "codex": CodexAdapter, "pi": PiAdapter}
AGENTS = tuple(ADAPTER_CLASSES)


def make_adapters(home: Path) -> dict[str, Adapter]:
    return {name: cls(home) for name, cls in ADAPTER_CLASSES.items()}


__all__ = ["ADAPTER_CLASSES", "AGENTS", "Adapter", "SessionFile", "make_adapters"]
