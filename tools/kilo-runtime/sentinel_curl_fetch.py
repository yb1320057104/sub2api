"""Transport interface for the already verified Sentinel bridge assets."""
from __future__ import annotations

import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from requests_fetch import fetch


def main() -> None:
    raw = sys.stdin.buffer.read(2 * 1024 * 1024 + 1)
    try:
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError
        result = fetch(json.loads(raw))
        if result.get("error_code"):
            raise ValueError
        sys.stdout.write(json.dumps({"status": result["status"], "body": result["body"]}))
    except Exception:
        sys.stdout.write('{"status":503,"body":""}')
        raise SystemExit(1)


if __name__ == "__main__":
    main()
