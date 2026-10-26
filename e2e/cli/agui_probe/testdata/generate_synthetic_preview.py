#!/usr/bin/env python3
"""Build an invented original-message paging response without captured input."""

import json
from pathlib import Path


def fixture():
    native = {
        "body": "demo", "messageId": "synthetic-message", "returned": 4, "remaining": 12,
        "continuationHint": "use message-show for the next byte range",
        "continuation": {"hasMore": True, "returned": 4, "remaining": 12,
                         "nextRange": {"bytes": {"offset": 4, "length": 4}}},
        "nextArgs": {"messageId": "synthetic-message", "byteRange": {"from": 4, "to": 8}},
    }
    return {"messages": [{"role": "tool", "content": json.dumps(native, separators=(",", ":"))}]}


if __name__ == "__main__":
    Path(__file__).with_name("synthetic-native-preview.json").write_text(json.dumps(fixture(), indent=2) + "\n")
