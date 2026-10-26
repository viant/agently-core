# Public source review

Publish generic framework behavior and independently constructed synthetic examples.
Keep live MCP output, business records, deployment settings, captures, investigation
notes, and credentials outside this repository. Commit messages must describe the
generic behavior without embedding investigation details or response values.

Before publishing, stage the intended files and run:

```sh
python3 -m unittest discover -s scripts -p 'test_public_boundaries.py'
python3 scripts/check_public_boundaries.py --base <reviewed-clean-ancestor>
python3 scripts/check_public_boundaries.py --base <reviewed-clean-ancestor> --head HEAD
```

The second command checks the index. The third checks every outgoing commit and
its message, including intermediate trees that a later deletion cannot clean.
Ignored output paths are also rejected if force-added. New or changed standalone
JSON test fixtures and JSON examples require a reviewed SHA-256 attestation of independently
constructed synthetic provenance in `service/agent/testdata/synthetic-fixtures.json`.
Review the contents before adding or updating an attestation; a checksum alone
does not establish provenance. Use reserved example domains and portable paths.

The check reports rule names and source paths without echoing matched values.
It supplements manual review of the complete outgoing diff and history; it cannot
identify every possible business record or private identifier. Rewriting a public
branch does not erase hosting caches, pull request refs, clones, or forks; exposed
history may also require repository-maintainer and hosting-support cleanup.
