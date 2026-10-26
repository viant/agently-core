#!/usr/bin/env python3
"""Construct small report evidence from public wire contracts, without inputs.

Run from any directory. This generator never reads captures or existing fixtures.
Compatibility tool/version names below are required by the public Go contract.
All identities, selectors, dates, operations, and numeric results are invented.
"""

from copy import deepcopy
import hashlib
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parent
SCOPE = {"ownerId": "synthetic-owner", "conversationId": "synthetic-conversation", "turnId": "synthetic-turn"}
DATES = ["2000-01-02", "2000-01-03", "2000-01-04"]


def digest(value):
    raw = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def call(operation, tool, request, response):
    return {**SCOPE, "opId": operation, "messageId": operation + "-message", "tool": tool, "status": "completed", "request": request, "response": response}


def fixtures():
    templates, columns, records = {}, [], []
    expected = [{"date": day} for day in DATES]
    for column in range(1, 6):
        key = "column" + str(column)
        template = {"dimensions": {}, "measures": {"value": True}, "filters": {"entityId": [7], "category": [column]}}
        templates[key] = template
        references = []
        for day_index, day in enumerate(DATES, 1):
            operation = f"synthetic-{key}-day{day_index}"
            request = deepcopy(template)
            request["filters"]["date"] = day + "T00:00:00Z"
            value = column * 100 + day_index
            response = {"status": "ok", "data": [{"eventDate": request["filters"]["date"], "value": value}]}
            records.append(call(operation, "sample/MetricSource", request, response))
            references.append({"opId": operation, "requestHash": digest(request), "responsePointer": "/data/0/value"})
            expected[day_index - 1][key] = value
        columns.append({"key": key, "calls": references})
    # Unbound successful operations must never be associated by position.
    for extra in range(5):
        request = deepcopy(templates["column1"])
        request["filters"]["date"] = DATES[0] + "T00:00:00Z"
        records.insert(extra * 3, call(f"synthetic-unbound-{extra}", "sample/MetricSource", request, {"status": "ok", "data": [{"value": 900 + extra}]}))
    incorrect = deepcopy(expected)
    incorrect[0]["column1"] += 1
    matrix = {"scope": SCOPE, "policy": {"sourceTool": "sample/MetricSource", "dates": DATES, "templates": templates}, "bindings": {"profile": "daily-metric-v1", "columns": columns}, "records": records, "incorrectAuthoredData": incorrect, "expectedData": expected}
    profile = {"entityId": 7, "entity": {"id": 7, "name": "Synthetic record", "target": "entityId=7", "exclusion": ""}}
    arguments = {"from": DATES[0], "to": DATES[-1], "inclusion": profile["entity"]["target"], "exclusion": "", "evidenceProfile": profile, "evidenceEntityId": 7}
    projection = {"version": 1, "producerVersion": "sample-profile-v1", "entityIds": [7], "sourceProfileHash": digest(profile), "inclusion": arguments["inclusion"], "exclusion": "", "templates": templates}
    sources = {
        "profile": call("synthetic-profile", "sample/EntityProfile", {"EntityId": [7]}, {"status": "ok", "data": [profile]}),
        "conversion": call("synthetic-conversion", "sample/NormalizeSelection", {"Request": arguments}, {"status": "ok", "body": {"from": DATES[0], "to": DATES[-1]}, "reportEvidence": projection}),
    }
    return {"synthetic_metric_calls.json": matrix, "synthetic_profile_projection.json": sources}


def main():
    for filename, value in fixtures().items():
        (ROOT / filename).write_text(json.dumps(value, indent=2) + "\n")


if __name__ == "__main__":
    main()
