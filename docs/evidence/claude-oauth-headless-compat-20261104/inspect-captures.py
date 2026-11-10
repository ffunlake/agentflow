"""Read only this acceptance run's captures and show protocol shape."""
import json
from pathlib import Path

for path in sorted(Path(__file__).parent.glob("headless-*.json")):
    if not path.name.startswith(("headless-start-", "headless-result-")):
        continue
    cap = json.loads(path.read_text())
    print(path.name)
    for event in map(json.loads, cap["stdout"].splitlines()):
        print(json.dumps({"type": event["type"], "subtype": event.get("subtype"), "keys": sorted(event),
            "message_keys": sorted(event.get("message", {})), "rate_limit_info": event.get("rate_limit_info"),
            "stop_reason": event.get("stop_reason"), "result": event.get("result"),
            "content": event.get("message", {}).get("content")}, indent=2))
