#!/usr/bin/env python3
"""
Backfill quality ratings from activity.jsonl into engram.

Reads all memory_eval entries that have resolved trace IDs and sends them
to engram's POST /v1/engrams/rate endpoint in chronological order.
Each session's ratings are sent as a separate batch so the EMA converges
correctly over time (same order as they originally occurred).

Usage:
    python3 scripts/backfill-quality-ratings.py [--dry-run] [--activity PATH] [--engram-url URL]
"""

import argparse
import json
import sys
import urllib.request
import urllib.error
from pathlib import Path

DEFAULT_ACTIVITY = Path.home() / "Documents/bud-state/system/activity.jsonl"
DEFAULT_ENGRAM_URL = "http://localhost:8080"


def load_eval_sessions(activity_path: Path) -> list[dict]:
    """Return list of {ts, resolved} dicts from memory_eval entries that have resolved IDs."""
    sessions = []
    with open(activity_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if entry.get("type") != "memory_eval":
                continue
            resolved = entry.get("data", {}).get("resolved")
            if not resolved:
                continue
            # resolved is {trace_id: rating} — filter to int ratings 1-5
            valid = {k: int(v) for k, v in resolved.items() if 1 <= int(v) <= 5}
            if valid:
                sessions.append({"ts": entry.get("ts", ""), "resolved": valid})
    return sessions


def post_ratings(engram_url: str, ratings: dict[str, int]) -> bool:
    """POST ratings to /v1/engrams/rate. Returns True on success."""
    body = json.dumps({"ratings": ratings}).encode()
    req = urllib.request.Request(
        f"{engram_url}/v1/engrams/rate",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status == 200
    except urllib.error.HTTPError as e:
        print(f"  HTTP {e.code}: {e.read().decode()[:200]}", file=sys.stderr)
        return False
    except Exception as e:
        print(f"  Error: {e}", file=sys.stderr)
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dry-run", action="store_true", help="Print what would be sent without calling engram")
    parser.add_argument("--activity", type=Path, default=DEFAULT_ACTIVITY, help="Path to activity.jsonl")
    parser.add_argument("--engram-url", default=DEFAULT_ENGRAM_URL, help="Engram base URL")
    args = parser.parse_args()

    if not args.activity.exists():
        print(f"Activity log not found: {args.activity}", file=sys.stderr)
        sys.exit(1)

    sessions = load_eval_sessions(args.activity)
    if not sessions:
        print("No memory_eval entries with resolved trace IDs found.")
        return

    total_ratings = sum(len(s["resolved"]) for s in sessions)
    unique_ids = len({k for s in sessions for k in s["resolved"]})
    print(f"Found {len(sessions)} rating sessions, {total_ratings} total ratings, {unique_ids} unique engram IDs")

    if args.dry_run:
        print("\n[dry-run] Sessions that would be sent:")
        for s in sessions:
            print(f"  {s['ts']}: {s['resolved']}")
        return

    sent = 0
    failed = 0
    for s in sessions:
        ok = post_ratings(args.engram_url, s["resolved"])
        if ok:
            sent += len(s["resolved"])
        else:
            failed += len(s["resolved"])
            print(f"  Failed session {s['ts']}", file=sys.stderr)

    print(f"\nDone: {sent} ratings sent, {failed} failed")
    if failed == 0:
        print("All historical ratings applied to engram quality scores.")


if __name__ == "__main__":
    main()
