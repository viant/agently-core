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
CHECKOUT = re.compile(r"/(?:Users|home)/[^/\s\"'`]+(?:/|(?=[\s\"'`]|$))")
PRIVATE_URL = re.compile(r"(?i)(?:https?|wss?)://[^\s/\"']*\.(?:internal|corp|private|viant\.ai)(?=[:/\s\"']|$)")
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


def content_issues(path, text, allow_synthetic_userinfo=False):
    result = []
    if CHECKOUT.search(text):
        result.append("private-checkout-path")
    if PRIVATE_URL.search(text) or (URL_CREDENTIALS.search(text) and not allow_synthetic_userinfo):
        result.append("private-deployment-url")
    if path == "go.mod" and LOCAL_REPLACE.search(text):
        result.append("local-module-replacement")
    return result


def allows_synthetic_userinfo(path, raw, entries):
    entry = entries.get(path, {})
    return (path.endswith("_test.go")
            and entry.get("origin") == "independently-constructed-synthetic"
            and entry.get("purpose") == "security-negative-example"
            and entry.get("sha256") == hashlib.sha256(raw).hexdigest())


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


def response_records(raw):
    """Recognize persisted request/response evidence, independent of its filename."""
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        return False

    def walk(item):
        if isinstance(item, dict):
            if "request" in item and "response" in item:
                return True
            return any(walk(child) for child in item.values())
        return isinstance(item, list) and any(walk(child) for child in item)

    return walk(value)


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL)


def tree_files(repo, tree):
    entries = git(repo, "ls-tree", "-r", "-z", tree).split(b"\0")
    blobs = []
    for entry in filter(None, entries):
        metadata, path = entry.split(b"\t", 1)
        _, kind, oid = metadata.split()
        if kind == b"blob":
            blobs.append((path.decode(), oid))
    batch = subprocess.check_output(["git", "-C", str(repo), "cat-file", "--batch"], input=b"".join(oid + b"\n" for _, oid in blobs), stderr=subprocess.DEVNULL)
    result, offset = {}, 0
    for path, _ in blobs:
        end = batch.index(b"\n", offset)
        size = int(batch[offset:end].rsplit(b" ", 1)[1])
        offset = end + 1
        result[path] = batch[offset:offset + size]
        offset += size + 1
    return result


def audit_tree(repo, base, revision=None, worktree=False):
    """Inspect complete tree content, including unchanged baseline source."""
    tree = None if worktree else revision or git(repo, "write-tree").decode().strip()
    if worktree:
        paths = git(repo, "ls-files", "--cached", "--others", "--exclude-standard", "-z").decode().split("\0")
        paths = sorted({path for path in paths if path and (repo / path).is_file()})
        changed = set(git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z", base).decode().split("\0"))
        changed.update(git(repo, "ls-files", "--others", "--exclude-standard", "-z").decode().split("\0"))
        read = lambda path: (repo / path).read_bytes()
    else:
        files = tree_files(repo, tree)
        paths = files.keys()
        changed = set(git(repo, "diff", "--name-only", "--diff-filter=ACMR", "-z", base, tree).decode().split("\0"))
        read = lambda path: files[path]
    issues = [(p, rule) for p in paths if p for rule in path_issues(p)]
    try:
        entries = json.loads(read(PROVENANCE))
    except (subprocess.CalledProcessError, ValueError, FileNotFoundError, KeyError):
        entries = {}
    for path in filter(None, paths):
        raw = read(path)
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            if path in changed:
                issues.append((path, "new-binary-needs-manual-privacy-review"))
            continue
        issues.extend((path, rule) for rule in content_issues(path, text, allows_synthetic_userinfo(path, raw, entries)))
        if path in changed or (path.endswith(".json") and response_records(raw)):
            issues.extend((path, rule) for rule in fixture_issues(path, raw, entries))
    return issues


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True, help="reviewed clean ancestor")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--head", help="audit every outgoing commit through this revision; default: index")
    selection.add_argument("--worktree", action="store_true", help="audit current tracked/unignored source before staging")
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
        issues = audit_tree(repo, args.base, revision, args.worktree)
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
