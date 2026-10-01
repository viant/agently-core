"""Shared paths and exact contract inventory for the root Datly tooling."""
import json
import re
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[2]


def contract_inventory(root=PROJECT_ROOT):
    module = re.search(r"^module\s+(\S+)", (root / "go.mod").read_text(), re.M)[1]
    inventory = json.loads((root / "migration/package-relocation-map.json").read_text())["contracts"]
    sources, destinations = set(), set()
    for item in inventory:
        source, destination = Path(item["new_dql"]), Path(item["new_generated"])
        if source.is_absolute() or ".." in source.parts or source.parts[0] != "dql":
            raise SystemExit("invalid DQL source in relocation map: " + str(source))
        if destination.is_absolute() or ".." in destination.parts or destination.parts[:2] != ("internal", "datly"):
            raise SystemExit("invalid generated destination in relocation map: " + str(destination))
        if item["new_package"] != module + "/" + destination.as_posix():
            raise SystemExit("generated package does not match root module: " + str(destination))
        if source in sources or destination in destinations:
            raise SystemExit("duplicate source/destination in contract inventory")
        sources.add(source)
        destinations.add(destination)
    discovered = {p.relative_to(root) for p in (root / "dql").rglob("*.dql")}
    if discovered != sources:
        raise SystemExit("DQL inventory differs from relocation map: " + str(sorted(str(p) for p in discovered ^ sources)))
    return module, inventory


def generated_files(root=PROJECT_ROOT):
    _, inventory = contract_inventory(root)
    return sorted({p for item in inventory for p in (root / item["new_generated"]).rglob("*") if p.is_file()})
