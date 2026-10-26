#!/usr/bin/env python3
"""Check publishable Git trees without printing potentially private content."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys


PRIVATE_DIRECTORIES = {"output", ".cache", "captures", "private-evidence"}
PROVENANCE = "service/agent/testdata/synthetic-fixtures.json"
CHECKOUT = re.compile(r"/(?:Users|home)/[^/\s\"']+/")
PRIVATE_URL = re.compile(r"(?i)(?:https?|wss?)://[^\s/\"']*\.(?:internal|corp|private)(?=[:/\s\"']|$)")
URL_CREDENTIALS = re.compile(r"(?i)(?:https?|wss?)://[^\s/:]+:[^\s/@]+@")
LOCAL_REPLACE = re.compile(r"(?m)^\s*(?:replace\s+)?[^\n]*=>\s*(?:\.{1,2}[/\\]|/|[A-Za-z]:[/\\])")
EVIDENCE_MESSAGE = re.compile(r"(?i)\b(?:internal|private)\s+(?:investigation|checkpoint|capture)\b|\blive\s+MCP\s+(?:response|capture)\b")


def path_issues(path):
    parts = PurePosixPath(path).parts
    result = []
    if parts and parts[0] in PRIVATE_DIRECTORIES:
        result.append("private-artifact-path")
    if path.endswith(".test"):
        result.append("generated-test-binary")
    if re.search(r"(?i)(?:^|/)(?:mcp[-_](?:capture|checkpoint|response)|live[-_]response)[^/]*\.(?:json|jsonl|ndjson)$", path):
        result.append("captured-response-path")
    return result


def content_issues(path, text):
    result = []
    if CHECKOUT.search(text):
        result.append("private-checkout-path")
    if PRIVATE_URL.search(text) or URL_CREDENTIALS.search(text):
        result.append("private-deployment-url")
    if path == "go.mod" and LOCAL_REPLACE.search(text):
        result.append("local-module-replacement")
    return result


def message_issues(message):
    result = content_issues("commit-message", message)
    if EVIDENCE_MESSAGE.search(message):
        result.append("investigation-commit-message")
    if re.search(r'"(?:jsonrpc|access_token|refresh_token)"\s*:', message):
        result.append("response-in-commit-message")
    return result


def fixture_issues(path, raw, entries):
    """New standalone test data requires a reviewed, content-bound attestation."""
    fixture = "/testdata/" in path or path.startswith("examples/")
    if not fixture or not path.endswith(".json") or path == PROVENANCE:
        return []
    entry = entries.get(path, {})
    if entry.get("origin") != "independently-constructed-synthetic":
        return ["missing-synthetic-provenance"]
    if entry.get("sha256") != hashlib.sha256(raw).hexdigest():
        return ["synthetic-fixture-changed-review-required"]
    return []


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL)


def audit_tree(repo, base, revision=None):
    """None selects the index; committed revisions are inspected independently."""
    tree = revision or git(repo, "write-tree").decode().strip()
    paths = git(repo, "ls-tree", "-r", "--name-only", "-z", tree).decode().split("\0")
    changed = git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z", base, tree).decode().split("\0")
    issues = [(p, rule) for p in paths if p for rule in path_issues(p)]
    try:
        entries = json.loads(git(repo, "show", tree + ":" + PROVENANCE))
    except (subprocess.CalledProcessError, ValueError):
        entries = {}
    for path in filter(None, changed):
        raw = git(repo, "show", tree + ":" + path)
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            issues.append((path, "new-binary-needs-manual-privacy-review"))
            continue
        issues.extend((path, rule) for rule in content_issues(path, text))
        issues.extend((path, rule) for rule in fixture_issues(path, raw, entries))
    return issues


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True, help="reviewed clean ancestor")
    parser.add_argument("--head", help="audit every outgoing commit through this revision; default: index")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    if args.head:
        if subprocess.run(["git", "-C", str(repo), "merge-base", "--is-ancestor", args.base, args.head], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            parser.error("base must be an ancestor of head")
        revisions = git(repo, "rev-list", "--reverse", args.base + ".." + args.head).decode().splitlines()
    else:
        revisions = [None]
    count = 0
    for revision in revisions:
        issues = audit_tree(repo, args.base, revision)
        if revision:
            message = git(repo, "show", "-s", "--format=%B", revision).decode()
            issues.extend(("commit-message", rule) for rule in message_issues(message))
        for path, rule in issues:
            # Never echo matching values, response bodies, or commit messages.
            print(f"{(revision or 'index')[:12]} {path}: {rule}", file=sys.stderr)
        count += len(issues)
    print(f"Public boundary check: {len(revisions)} tree(s), {count} finding(s).")
    return bool(count)


if __name__ == "__main__":
    sys.exit(main())
