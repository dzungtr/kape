# runtime/tests/test_dedup.py
from kape_runtime.dedup import DedupWindow


def test_first_event_not_duplicate():
    dedup = DedupWindow(ttl_seconds=60)
    assert dedup.is_duplicate("evt-001") is False
    assert "evt-001" in dedup._seen


def test_same_id_within_ttl_is_duplicate():
    dedup = DedupWindow(ttl_seconds=60)
    assert dedup.is_duplicate("evt-001") is False
    assert dedup.is_duplicate("evt-001") is True


def test_expired_id_not_duplicate(monkeypatch):
    import kape_runtime.dedup as dedup_mod

    fake_now = {"value": 1000.0}
    monkeypatch.setattr(dedup_mod.time, "monotonic", lambda: fake_now["value"])

    dedup = dedup_mod.DedupWindow(ttl_seconds=60)
    assert dedup.is_duplicate("evt-001") is False

    # Advance time past TTL
    fake_now["value"] = 1100.0
    assert dedup.is_duplicate("evt-001") is False


def test_sweep_removes_expired(monkeypatch):
    import kape_runtime.dedup as dedup_mod

    fake_now = {"value": 1000.0}
    monkeypatch.setattr(dedup_mod.time, "monotonic", lambda: fake_now["value"])

    dedup = dedup_mod.DedupWindow(ttl_seconds=60)
    dedup.is_duplicate("evt-a")
    dedup.is_duplicate("evt-b")
    dedup.is_duplicate("evt-c")
    assert len(dedup._seen) == 3

    # Advance past TTL and check a new id — sweep should clear the old ones
    fake_now["value"] = 1100.0
    dedup.is_duplicate("evt-d")
    assert len(dedup._seen) == 1
    assert "evt-d" in dedup._seen
