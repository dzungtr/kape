# runtime/src/kape_runtime/dedup.py
from __future__ import annotations

import time


class DedupWindow:
    """Sliding-window dedup cache: tracks event_id → expiry timestamp."""

    def __init__(self, ttl_seconds: int = 60) -> None:
        self._seen: dict[str, float] = {}
        self._ttl = ttl_seconds

    def is_duplicate(self, event_id: str) -> bool:
        now = time.monotonic()
        expired = [k for k, exp in self._seen.items() if exp <= now]
        for k in expired:
            del self._seen[k]
        if event_id in self._seen:
            return True
        self._seen[event_id] = now + self._ttl
        return False
