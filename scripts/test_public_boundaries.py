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


if __name__ == "__main__":
    unittest.main()
