"""Offline checks for release evidence, capture validation, and rule review."""

import copy
import io
import os
import re
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from zipfile import ZipFile

import releases
import workflow_support
from capture import CREDENTIAL, Collector, LANES, publish
from protocols import CATALOG_HEADER, RESULT_TOKEN, code_mode, has_fixture_result, response, sse, trace_tool
from report import compare, report, rule_candidates, streamed_tool, validate


def pi_trace(version, identity, tool_name):
    protocol, model = LANES["pi"]
    system = {"role": "system", "content": identity + "\nCall `" + tool_name + "` to read the fixture."}
    user = {"role": "user", "content": "Read the trace fixture."}
    body = {
        "messages": [system, user],
        "tools": [{"type": "function", "function": {
            "name": tool_name, "description": f"Use {tool_name} in Pi.",
            "parameters": {"type": "object", "properties": {"path": {"type": "string", "description": "Pi fixture."}},
                           "required": ["path"]},
        }}],
    }
    round_two = copy.deepcopy(body)
    round_two["messages"].append({"role": "tool", "content": RESULT_TOKEN, "tool_call_id": "trace_tool_call"})
    return {
        "schema": 1, "config_revision": 1, "failures": [],
        "release": {"lane": "pi", "version": version, "running_version": version},
        "scenarios": [
            {"scenario": "text", "status": "captured", "protocol": protocol, "model": model,
             "requests": [publish(body, "/var/tmp/fixture")], "tool_call": None},
            {"scenario": "read", "status": "captured", "protocol": protocol, "model": model,
             "requests": [publish(body, "/var/tmp/fixture"), publish(round_two, "/var/tmp/fixture")],
             "tool_call": {"name": tool_name, "arguments": {"path": "/trace/project/trace-example.txt"},
                           "declaration": "/tools/0/function"}},
        ],
    }


class ReleaseTests(unittest.TestCase):
    def test_release_source_and_integrity(self):
        metadata = {"name": "opencode-ai", "version": "1.2.3", "dist": {
            "tarball": "https://registry.npmjs.org/opencode-ai/-/opencode-ai-1.2.3.tgz", "integrity": "sha512-proof",
        }}
        with patch.object(releases, "fetch_json", return_value=metadata) as fetch:
            manifest = releases.resolve("opencode-v1")
        self.assertEqual(fetch.call_args.args, ("https://registry.npmjs.org/opencode-ai/latest",))
        self.assertEqual((manifest["version"], manifest["integrity"]), ("1.2.3", "sha512-proof"))
        with patch.object(releases, "fetch_json", return_value={**metadata, "version": "1.2.3-beta.1"}):
            with self.assertRaisesRegex(ValueError, "stable version"):
                releases.resolve("opencode-v1")

    def test_v2_selects_linux_baseline_asset(self):
        asset = "https://opencode.ai/files/bin/2.3.2/opencode-linux-x64-baseline.tar.gz"
        formula = f'version "2.3.2"\nurl "{asset}"\nsha256 "{"a" * 64}"'
        self.assertEqual(releases.open_code_asset(formula), ("2.3.2", asset, "a" * 64))
        with self.assertRaisesRegex(ValueError, "matching Linux"):
            releases.open_code_asset(formula.replace("/bin/2.3.2/", "/bin/2.3.1/"))


class CaptureTests(unittest.TestCase):
    def test_publish_retains_contract_and_redacts_scratch_and_metadata(self):
        scratch = Path(tempfile.gettempdir()) / "trace-test-secret"
        project_slug = re.sub(r"[^A-Za-z0-9]", "-", str(scratch)) + "-project"
        sample = {
            "metadata": {"headers": {"Authorization": CREDENTIAL}},
            "system": f"OpenCode uses {scratch}/project.\nClaude memory {scratch}/home/.claude/projects/{project_slug}/memory.\nToday's date: Yesterday",
            "tools": [{"name": "read", "parameters": {"properties": {"api_key": {"type": "string"}}}}],
            "messages": [{"role": "user", "content": CREDENTIAL}],
        }
        published = publish(sample, scratch)["body"]
        self.assertEqual(published["system"], "OpenCode uses /trace/project.\nClaude memory /trace/home/.claude/projects/-trace-project/memory.\nToday's date: <trace-date>")
        self.assertEqual(published["messages"][0]["content"], "<redacted>")
        self.assertEqual(published["tools"], sample["tools"])
        self.assertNotIn("metadata", published)

    def test_only_tool_result_counts(self):
        self.assertFalse(has_fixture_result("chat", {"messages": [{"role": "user", "content": RESULT_TOKEN}]}))
        trace = pi_trace("1.0.0", "You are Pi.", "pi_read")
        validate(trace)
        trace["scenarios"][1]["requests"][1] = trace["scenarios"][0]["requests"][0]
        with self.assertRaisesRegex(ValueError, "completed tool round trip"):
            validate(trace)
        trace["failures"] = [{"stage": "install", "reason": "release unavailable"}]
        with self.assertRaisesRegex(ValueError, "capture failures"):
            validate(trace)

    def test_empty_capture_cannot_become_a_baseline(self):
        trace = pi_trace("1.0.0", "You are Pi.", "pi_read")
        trace["scenarios"][0]["requests"] = []
        with self.assertRaisesRegex(ValueError, "empty requests"):
            validate(trace)

    def test_code_mode_supports_observed_multiline_single_member(self):
        body = {
            "messages": [{"role": "system", "content": f"# Code Mode\n{CATALOG_HEADER}\n"
                          "- read (1 tool)\n  - tools.read({\n  /** File to read */\n  path: string,\n}): Promise<string>"}],
            "tools": [{"type": "function", "function": {"name": "execute"}}],
        }
        self.assertTrue(code_mode(body))
        tool = trace_tool(body, Path("/trace/project/trace-example.txt"), "code-mode")
        self.assertEqual(tool["declaration"], "/tools/0/function")
        self.assertEqual(tool["arguments"]["code"], 'return await tools.read({ path: "/trace/project/trace-example.txt" });')

    def test_auxiliary_model_request_does_not_consume_tool_turn(self):
        body = pi_trace("1.0.0", "You are Pi.", "read")["scenarios"][1]["requests"][0]["body"]
        with Collector("chat", "gpt-4.1", Path("/trace/project/trace-example.txt"), "read") as collector:
            self.assertIsNone(collector.next_response({"messages": [{"role": "system", "content": "Generate a title"}]}))
            self.assertEqual(collector.next_response(body)["name"], "read")
            self.assertEqual(len(collector.requests), 1)
            self.assertEqual(collector.auxiliary, 1)


class ReviewTests(unittest.TestCase):
    def test_claude_vendor_drift_is_a_removal_review_candidate(self):
        changed = "- Fast mode for Claude Code can switch to a model from the configured roster."
        candidate, = rule_candidates("claude", "/system/2/text", changed)
        self.assertEqual(candidate["kind"], "vendor-content-review")
        self.assertEqual(candidate["match"], changed)

    def test_version_drift_reports_candidates_with_evidence(self):
        baseline = pi_trace("1.0.0", "You are Pi.", "pi_fetch")
        current = pi_trace("1.1.0", "You are Pi.\nYou are PiAgent.\nconst PiAgent = plugin;", "pi_read")
        changes = compare(current, baseline)
        templates = [(item, candidate) for item in changes for candidate in item.get("candidates", [])
                     if candidate["kind"] == "identity-template" and "PiAgent" in candidate["match"]]
        self.assertTrue(templates)
        item, template = templates[0]
        self.assertEqual((item["path"], item["old_version"], item["new_version"]), ("/messages/0/content", "1.0.0", "1.1.0"))
        self.assertEqual(len(item["request_sha256"]), 64)
        self.assertEqual(template["match"], "You are PiAgent.")
        self.assertTrue(any(c["kind"] == "preserve" and "const PiAgent" in c["match"]
                            for item in changes for c in item.get("candidates", [])))
        self.assertTrue(any(c["kind"] == "tool-alias" and c["match"] == "pi_read"
                            for item in changes for c in item.get("candidates", [])))
        self.assertEqual(compare(current, current), [])

    def test_only_comparable_config_establishes_drift(self):
        baseline = pi_trace("1.0.0", "You are Pi.", "pi_fetch")
        other = {**baseline, "config_revision": 2}
        found = compare(other, baseline)
        self.assertTrue(all(item.get("old_version") is None for item in found))
        self.assertEqual(sum(item.get("status") == "baseline-created" for item in found), 2)

    def test_sse_tool_arguments_survive_double_encoding(self):
        code = 'return await tools.read({ path: "/trace/project/trace-example.txt" });'
        for protocol in ("chat", "responses", "gemini", "messages"):
            with self.subTest(protocol=protocol):
                _, events = response(protocol, "fixture", {"name": "execute", "arguments": {"code": code}})
                self.assertEqual(streamed_tool(protocol, sse(protocol, events)), ("execute", "", {"code": code}))

    @unittest.skipUnless(os.environ.get("TRACE_TEST_BRIDGE"), "set TRACE_TEST_BRIDGE to test native replay")
    def test_abi_replays_version_drift_and_restores_client_tool(self):
        baseline = pi_trace("1.0.0", "You are Pi.", "pi_fetch")
        trace = pi_trace("1.1.0", "You are Pi.\nYou are PiAgent.", "pi_read")
        library = Path(os.environ["TRACE_TEST_BRIDGE"]).resolve()
        checked = report(trace, baseline, library)
        self.assertEqual(checked["failures"], [])
        first = checked["replays"][1]["rounds"][0]
        self.assertTrue(first["selected_tool_round_trip"])
        self.assertEqual(first["aliases"][0]["upstream"], "antigravity_read")
        self.assertEqual(first["aliases"][0]["original"], "pi_read")


class BaselineTests(unittest.TestCase):
    def test_skip_current_expired_and_wrong_workflow_artifacts(self):
        def artifact(n, branch="main", expired=False):
            return {"id": n, "created_at": f"2026-11-{n:02d}", "expired": expired,
                    "size_in_bytes": 512, "workflow_run": {"id": n, "head_branch": branch}}

        artifacts = {"artifacts": [artifact(8), artifact(7, expired=True), artifact(6, branch="other"), artifact(5), artifact(4)]}

        def api(path):
            if "artifacts?" in path:
                self.assertIn("agent-trace-baseline-pi", path)
                return artifacts
            if path.endswith("/5"):
                return {"path": ".github/workflows/ci.yml", "status": "completed"}
            self.assertTrue(path.endswith("/4"))
            # One failed matrix lane must not discard the others' baselines.
            return {"path": ".github/workflows/agent-traces.yml", "status": "completed", "conclusion": "failure"}

        archive = io.BytesIO()
        with ZipFile(archive, "w") as zipped:
            zipped.writestr("trace.json", b'{"baseline":true}')
        archive.seek(0)
        with patch.object(workflow_support, "github_json", side_effect=api), patch.object(workflow_support, "github", return_value=archive):
            self.assertEqual(workflow_support.previous_baseline("pi", "owner/repo", "main", "8"), b'{"baseline":true}')


if __name__ == "__main__":
    unittest.main()
