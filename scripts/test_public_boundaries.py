import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("boundaries", Path(__file__).with_name("check_public_boundaries.py"))
boundaries = importlib.util.module_from_spec(spec)
spec.loader.exec_module(boundaries)


class PublicBoundaryTests(unittest.TestCase):
    def test_artifacts_are_rejected_even_if_force_added(self):
        for path in ["output/run.json", ".cache/results.json", "captures/frame.png", "private-evidence/notes.md", "runner.test", "testdata/mcp-response-demo.json"]:
            with self.subTest(path=path):
                self.assertTrue(boundaries.path_issues(path))

    def test_generic_terms_and_synthetic_examples_are_allowed(self):
        for path in ["service/segments/parser.go", "camera/capture.go", "examples/records.json"]:
            self.assertFalse(boundaries.path_issues(path))
        self.assertFalse(boundaries.content_issues("examples/demo.md", 'https://fixture.invalid/api {"recordId":7,"name":"Example"}'))

    def test_private_checkout_in_example_or_fixture_is_rejected(self):
        checkout = "/" + "Users/demo/projects/private-app/data.json"
        for path in ["examples/README.md", "unit_test.go", "testdata/records.json"]:
            self.assertIn("private-checkout-path", boundaries.content_issues(path, checkout))
        self.assertIn("private-checkout-path", boundaries.content_issues("example.md", "/" + "Users/demo"))

    def test_private_urls_and_url_credentials_are_rejected(self):
        for url in ["https://api.demo" + ".internal/data", "wss://api.demo" + ".corp/socket", "https://demo:" + "synthetic-password@example.invalid"]:
            self.assertIn("private-deployment-url", boundaries.content_issues("examples/demo.yaml", url))

    def test_local_dependency_replacements_are_rejected(self):
        for target in ["../framework", "/tmp/framework", "C:\\work\\framework"]:
            self.assertIn("local-module-replacement", boundaries.content_issues("go.mod", "replace example.invalid/framework => " + target))
        self.assertFalse(boundaries.content_issues("go.mod", "replace example.invalid/framework => example.invalid/framework v1.2.3"))

    def test_portable_fixture_paths_are_allowed(self):
        self.assertFalse(boundaries.content_issues("unit_test.go", 'ReadFile("../../testdata/record.json")'))

    def test_commit_messages_reject_evidence_and_values_without_echo(self):
        for message in ["ENG-12345: internal " + "investigation checkpoint", 'ENG-12345: {"jsonrpc":"2.0","result":{"recordId":7}}', "ENG-12345: https://demo" + ".private/data"]:
            self.assertTrue(boundaries.message_issues(message))
        self.assertFalse(boundaries.message_issues("ENG-12345: preserve authorized UI state and portable authoring"))

    def test_new_json_fixture_requires_content_bound_synthetic_provenance(self):
        path, raw = "service/demo/testdata/records.json", b'{"recordId":7}'
        self.assertEqual(["missing-synthetic-provenance"], boundaries.fixture_issues(path, raw, {}))
        entries = {path: {"origin": "independently-constructed-synthetic", "sha256": hashlib.sha256(raw).hexdigest()}}
        self.assertFalse(boundaries.fixture_issues(path, raw, entries))
        self.assertEqual(["synthetic-fixture-changed-review-required"], boundaries.fixture_issues(path, b'{"recordId":8}', entries))

    def test_response_or_business_example_cannot_bypass_provenance(self):
        raw = b'{"jsonrpc":"2.0","result":{"records":[{"recordId":7,"name":"Synthetic Example"}]}}'
        for path in ["examples/records.json", "service/demo/testdata/response.json"]:
            self.assertEqual(["missing-synthetic-provenance"], boundaries.fixture_issues(path, raw, {}))

    def test_rule_diagnostics_do_not_return_matched_values(self):
        value = "https://demo:" + "synthetic-password@example.invalid"
        issues = boundaries.content_issues("examples/demo.md", value)
        self.assertEqual(["private-deployment-url"], issues)
        self.assertNotIn("synthetic-password", " ".join(issues))

    def test_security_negative_example_requires_exact_reviewed_source(self):
        path = "service/demo/security_test.go"
        raw = ("https://demo:" + "synthetic-password@example.invalid").encode()
        entries = {path: {"origin": "independently-constructed-synthetic", "purpose": "security-negative-example", "sha256": hashlib.sha256(raw).hexdigest()}}
        self.assertTrue(boundaries.allows_synthetic_userinfo(path, raw, entries))
        self.assertFalse(boundaries.content_issues(path, raw.decode(), True))
        self.assertFalse(boundaries.allows_synthetic_userinfo(path, raw + b"changed", entries))
        self.assertFalse(boundaries.allows_synthetic_userinfo("examples/demo.md", raw, entries))
        self.assertTrue(boundaries.content_issues(path, "/" + "Users/demo/workspace/example", True))

    def test_outgoing_tree_audit_catches_an_intermediate_capture(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL).decode().strip()
            git("init", "-q")
            git("config", "user.email", "fixture@example.invalid")
            git("config", "user.name", "Synthetic Test")
            (repo / "README.md").write_text("Generic framework\n")
            git("add", ".")
            git("commit", "-qm", "ENG-12345: initialize generic framework")
            base = git("rev-parse", "HEAD")
            (repo / "output").mkdir()
            (repo / "output/run.json").write_text(json.dumps({"recordId": 7}))
            git("add", ".")
            git("commit", "-qm", "ENG-12345: add synthetic sample")
            captured = git("rev-parse", "HEAD")
            git("rm", "-q", "output/run.json")
            git("commit", "-qm", "ENG-12345: remove sample")
            self.assertIn(("output/run.json", "private-artifact-path"), boundaries.audit_tree(repo, base, captured))
            self.assertFalse(boundaries.audit_tree(repo, base, "HEAD"))

    def test_unchanged_baseline_source_and_response_records_are_checked(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL).decode().strip()
            git("init", "-q")
            git("config", "user.email", "fixture@example.invalid")
            git("config", "user.name", "Synthetic Test")
            (repo / "README.md").write_text("/" + "Users/demo/workspace/example\n")
            (repo / "service/demo/testdata").mkdir(parents=True)
            (repo / "service/demo/testdata/evidence.json").write_text('{"records":[{"request":{"recordId":7},"response":{"data":[1]}}]}')
            git("add", ".")
            git("commit", "-qm", "ENG-12345: add synthetic baseline for negative regression")
            base = git("rev-parse", "HEAD")
            issues = boundaries.audit_tree(repo, base, "HEAD")
            self.assertIn(("README.md", "private-checkout-path"), issues)
            self.assertIn(("service/demo/testdata/evidence.json", "missing-synthetic-provenance"), issues)
            self.assertEqual(issues, boundaries.audit_tree(repo, base, worktree=True))

    def test_synthetic_reporting_fixtures_reproduce_from_generator(self):
        directory = Path(__file__).resolve().parent.parent / "service/reporting/forecastbinding/testdata"
        spec = importlib.util.spec_from_file_location("synthetic_evidence", directory / "generate_synthetic_evidence.py")
        generator = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(generator)
        generated = generator.fixtures()
        self.assertEqual(2, len(generated))
        for filename, value in generated.items():
            self.assertEqual(json.dumps(value, indent=2) + "\n", (directory / filename).read_text())
        matrix = generated["synthetic_metric_calls.json"]
        self.assertEqual(20, len(matrix["records"]))
        self.assertEqual(15, sum(len(column["calls"]) for column in matrix["bindings"]["columns"]))
        self.assertTrue(all(call["ownerId"] == "synthetic-owner" for call in matrix["records"]))

    def test_synthetic_cli_preview_reproduces_from_generator(self):
        directory = Path(__file__).resolve().parent.parent / "e2e/cli/agui_probe/testdata"
        spec = importlib.util.spec_from_file_location("synthetic_preview", directory / "generate_synthetic_preview.py")
        generator = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(generator)
        value = generator.fixture()
        self.assertEqual(json.dumps(value, indent=2) + "\n", (directory / "synthetic-native-preview.json").read_text())
        native = json.loads(value["messages"][0]["content"])
        self.assertEqual("synthetic-message", native["messageId"])
        self.assertEqual(native["returned"], len(native["body"].encode()))

    def test_synthetic_authored_report_contract_and_reproducibility(self):
        root = Path(__file__).resolve().parent.parent
        subprocess.run(["node", str(root / "scripts/generate-synthetic-report-fixture.mjs"), "--check"], cwd=root, check=True, capture_output=True)
        fixture = json.loads((root / "service/reporting/testdata/synthetic_authored_report.json").read_text())
        self.assertEqual(6, len(fixture["dataSources"]))
        self.assertEqual(8, len(fixture["dependencies"]))
        self.assertTrue(all(value["backend"]["kind"] == "mcp_tool" for value in fixture["dataSources"].values()))


if __name__ == "__main__":
    unittest.main()
