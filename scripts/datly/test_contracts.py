"""Inventory safety gates: exact generated leaves, authored hooks, and host exclusion."""
import json
import tempfile
import unittest
from pathlib import Path
from contracts import contract_inventory, generated_files


class ContractInventoryTests(unittest.TestCase):
    def fixture(self, root):
        (root / 'go.mod').write_text('module example.test/core\n')
        (root / 'migration').mkdir()
        source = root / 'dql/item/write/writer.dql'
        source.parent.mkdir(parents=True)
        source.write_text("#package('example.test/core/internal/datly/item/write')\n")
        entry = dict(new_dql='dql/item/write/writer.dql', new_generated='internal/datly/item/write', new_package='example.test/core/internal/datly/item/write')
        (root / 'migration/package-relocation-map.json').write_text(json.dumps({'contracts': [entry]}))
        return entry

    def test_snapshot_includes_contract_and_hook_but_excludes_authored_host(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            for name in ['internal/datly/item/write/input.go', 'internal/datly/item/write/lifecycle.go', 'internal/datly/host/server.go', 'internal/store/item/store.go']:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('fixture')
            self.assertEqual([path.relative_to(root).as_posix() for path in generated_files(root)], ['internal/datly/item/write/input.go', 'internal/datly/item/write/lifecycle.go'])

    def test_unknown_dql_fails_before_generation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            (root / 'dql/extra.dql').write_text('unexpected')
            with self.assertRaisesRegex(SystemExit, 'inventory differs'):
                contract_inventory(root)

    def test_destination_outside_internal_generated_area_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            entry = self.fixture(root)
            entry['new_generated'] = 'pkg/agently/item'
            (root / 'migration/package-relocation-map.json').write_text(json.dumps({'contracts': [entry]}))
            with self.assertRaisesRegex(SystemExit, 'invalid generated destination'):
                contract_inventory(root)


if __name__ == '__main__':
    unittest.main()
