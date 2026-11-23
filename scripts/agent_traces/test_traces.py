"""Offline checks for release evidence, capture validation, and rule review."""

import copy
import io
import json
import os
import re
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib.error import HTTPError
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


def tagged_trace(lane, version, with_code_mode=False):
    protocol, model = LANES[lane]
    trace = pi_trace(version, "You are this fixture agent.", "read")
    trace["release"]["lane"] = lane
    body = copy.deepcopy(trace["scenarios"][0]["requests"][0]["body"])
    result = {
        "chat": {"messages": body["messages"] + [{"role": "tool", "content": RESULT_TOKEN}]},
        "responses": {"input": [{"type": "function_call_output", "output": RESULT_TOKEN}]},
        "messages": {"messages": body["messages"] + [
            {"role": "user", "content": [{"type": "tool_result", "content": RESULT_TOKEN}]}]},
        "gemini": {"contents": [{"parts": [{"functionResponse": {"response": {"text": RESULT_TOKEN}}}]}]},
    }[protocol]
    for scenario in trace["scenarios"]:
        scenario.update(protocol=protocol, model=model, requests=[publish(body, "/fixture")])
        if scenario["scenario"] == "read":
            scenario["requests"].append(publish({**body, **result}, "/fixture"))
    if lane.startswith("opencode"):
        entry = copy.deepcopy(trace["scenarios"][1])
        entry["scenario"] = "code-mode"
        if with_code_mode:
            for item in entry["requests"]:
                changed = item["body"]
                changed["messages"][0]["content"] += "\n" + CATALOG_HEADER
                changed["tools"].append({"name": "execute"})
            entry["requests"] = [publish(item["body"], "/fixture") for item in entry["requests"]]
        else:
            entry.update(status="unsupported", requests=entry["requests"][:1], tool_call=None)
        trace["scenarios"].append(entry)
    return trace


def lane_artifact(root, trace, attempt=1, stages=None, comparison=None, diff=None):
    lane = trace["release"]["lane"]
    path = root / f"agent-trace-lane-{lane}-{attempt}"
    path.mkdir(parents=True)
    for name, data in {
        "release": trace["release"], "trace": trace,
        "stages": stages if stages is not None else {"capture": "success", "replay": "success"},
        "comparison": comparison or {}, "report": {"failures": [], "diff": diff or [], "baseline_version": "1.0.0"},
    }.items():
        workflow_support.write_json(path / f"{name}.json", data)


class BaselineTests(unittest.TestCase):
    def test_tagged_baseline_lifecycle_and_idempotent_upload(self):
        tags = ["v0.5.3", "v0.5.4", "v0.5.5", "v0.99.0-rc.1"]
        commits = {"v0.5.4": "a" * 40, "v0.5.5": "b" * 40}
        assets = {}

        def github(path, accept=None):
            if "/releases/tags/" in path:
                tag = path.rsplit("/", 1)[-1]
                current = assets.get(tag)
                return io.BytesIO(json.dumps({"assets": [] if current is None else [{
                    "id": list(commits).index(tag) + 1, "size": len(current), "state": "uploaded",
                    "name": workflow_support.BASELINE_ASSET,
                }]}).encode())
            self.assertEqual(accept, "application/octet-stream")
            tag = list(commits)[int(path.rsplit("/", 1)[-1]) - 1]
            return io.BytesIO(assets[tag])

        def upload(command, check):
            self.assertTrue(check)
            self.assertEqual(command[:3], ["gh", "release", "upload"])
            tag = command[3]
            self.assertNotIn(tag, assets)
            assets[tag] = Path(command[4]).read_bytes()

        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), \
                patch.object(workflow_support, "github", side_effect=github), \
                patch.object(workflow_support.subprocess, "run", side_effect=upload) as sender:
            root = Path(temp)
            for index, (tag, commit) in enumerate(commits.items(), 1):
                version = f"{index}.0.0"
                tag_inputs = root / tag / "lanes"
                bundle = root / tag / "bundle"
                for lane in LANES:
                    lane_artifact(tag_inputs, tagged_trace(lane, version))
                baseline_tag = workflow_support.select_baseline_tag(tags, tag)
                comparison, baseline = workflow_support.compare_with_baseline(
                    tagged_trace("pi", version), baseline_tag, commits.get(baseline_tag, ""))
                if index == 1:
                    self.assertEqual((baseline_tag, comparison["status"], baseline), ("v0.5.3", "missing", None))
                else:
                    self.assertEqual((baseline_tag, comparison["status"], baseline["release"]["version"]),
                                     ("v0.5.4", "available", "1.0.0"))
                workflow_support.assemble_bundle(tag_inputs, bundle, LANES, 1, commit, tag, baseline_tag)
                destination = root / tag / workflow_support.BASELINE_ASSET
                workflow_support.publish_baseline(bundle, tag, commit, destination)
                with ZipFile(destination) as zipped:
                    metadata = json.loads(zipped.read("manifest.json"))
                    self.assertEqual((metadata["tag"], metadata["commit"]), (tag, commit))
                    self.assertEqual(set(metadata["lanes"]), set(LANES))
                    self.assertEqual(metadata["lanes"]["pi"]["version"], version)

            original = assets["v0.5.5"]
            workflow_support.publish_baseline(root / "missing", "v0.5.5", commits["v0.5.5"], root / "unused")
            self.assertEqual(sender.call_count, 2)
            self.assertEqual(assets["v0.5.5"], original)

            selected = workflow_support.select_baseline_tag(tags)
            current = tagged_trace("pi", "2.0.0")
            comparison, baseline = workflow_support.compare_with_baseline(current, selected, commits[selected])
            self.assertEqual((selected, comparison["status"], compare(current, baseline)), ("v0.5.5", "available", []))
            comparison, _ = workflow_support.compare_with_baseline(current, selected, "c" * 40)
            self.assertEqual(comparison["status"], "unreadable")
            current["config_revision"] += 1
            comparison, baseline = workflow_support.compare_with_baseline(current, selected, commits[selected])
            self.assertEqual((comparison["status"], baseline), ("incompatible", None))

    def test_transport_failure_is_reported(self):
        error = HTTPError("https://example.test", 403, "forbidden", {}, None)
        with patch.object(workflow_support, "release_asset", side_effect=error):
            comparison, baseline = workflow_support.compare_with_baseline(tagged_trace("pi", "1.0.0"), "v0.5.4", "sha")
        self.assertIsNone(baseline)
        self.assertEqual(comparison["status"], "unreadable")
        self.assertIn("403", comparison["reason"])

    def test_summary_shows_concrete_changes_and_partial_bundle(self):
        before = tagged_trace("opencode-v2", "1.0.0", with_code_mode=True)
        after = tagged_trace("opencode-v2", "2.0.0", with_code_mode=False)
        changed = after["scenarios"][0]["requests"][0]["body"]
        changed["tools"][0]["function"]["name"] = "read_file"
        after["scenarios"][0]["requests"][0] = publish(changed, "/fixture")
        difference = compare(after, before)
        comparison = {"tag": "v0.5.4", "status": "available"}
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            inputs, output = root / "lanes", root / "bundle"
            lane_artifact(inputs, before, 1)
            lane_artifact(inputs, after, 2, comparison=comparison, diff=difference)
            lane_artifact(inputs, tagged_trace("pi", "1.0.0"), 1, comparison=comparison)
            lane_artifact(inputs, tagged_trace("gemini", "1.0.0"), 2,
                          stages={"capture": "failure", "replay": "skipped"})
            lane_artifact(inputs, tagged_trace("codex", "1.0.0"), 2,
                          comparison={"status": "unreadable", "reason": "Baseline read failed (HTTP 403)."})
            lane_artifact(inputs, tagged_trace("claude", "1.0.0"), 2,
                          comparison={"status": "incompatible", "reason": "Capture configurations differ."})
            workflow_support.assemble_bundle(inputs, output, LANES, 2, "sha", "v0.5.5", "v0.5.4")
            summary = (output / "summary.md").read_text()
            for expected in ("capability-changed", "<code>unsupported</code>", "requests-removed",
                             "<code>2</code> | <code>1</code>", "/tools/0/function/name",
                             "<code>read</code> | <code>read_file</code>", "Failed stages · capture",
                             "Artifact from run attempt 1", "No lane artifact available.",
                             "Baseline read failed (HTTP 403).", "Capture configurations differ.",
                             "No changes in the captured request evidence."):
                self.assertIn(expected, summary)
            self.assertNotIn("template hits", summary)
            self.assertNotIn("tool aliases", summary)
            self.assertTrue((output / "opencode-v2" / "trace.json").is_file())
            with self.assertRaisesRegex(ValueError, "successful capture and replay"):
                workflow_support.build_baseline(output, "v0.5.5", "sha", root / "refused.zip")


if __name__ == "__main__":
    unittest.main()
