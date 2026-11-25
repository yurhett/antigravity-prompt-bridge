"""Compare captured prompts and replay tool aliases through the native CPA ABI."""

import argparse
import base64
import copy
import ctypes
import difflib
import hashlib
import json
import re
import sys
from pathlib import Path

from capture import LANES, SCENARIOS, canonical
from protocols import FORMATS, catalog_tools, code_mode, declarations, has_fixture_result, response, sse, system_fields

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from abi_support import NATIVE_IDENTITY, Plugin

BRANDS = {
    "opencode-v1": ("OpenCode",), "opencode-v2": ("OpenCode",),
    "codex": ("OpenAI Codex", "Codex CLI", "Codex"), "pi": ("PiAgent", "Pi"),
    "gemini": ("Gemini CLI",), "claude": ("Claude Code", "Claude Agent"),
}
SELF = re.compile(r"(?i)(?:\byou (?:are|operate in|run in)|\b(?:running (?:in|inside)|called|named)|你是)\s+(?:(?:a|an|the)\s+)?")
ESCAPE = re.compile(r"(?:\x1b\[[0-?]*[ -/]*[@-~]|[\x00-\x08\x0b-\x1f])")
DESCRIPTION_KEYS = {
    "properties", "patternProperties", "$defs", "definitions", "dependencies", "dependentSchemas",
}
SUBSCHEMA_KEYS = {
    "items", "prefixItems", "anyOf", "any_of", "allOf", "oneOf", "additionalItems",
    "additionalProperties", "contains", "propertyNames", "unevaluatedItems",
    "unevaluatedProperties", "not", "if", "then", "else",
}
SCHEMA_KEYS = {"parameters", "input_schema", "parametersJsonSchema", "parameters_json_schema"}
FAMILIES = {
    "opencode-v1": "OpenCode", "opencode-v2": "OpenCode", "codex": "Codex", "pi": "Pi",
    "gemini": "Gemini CLI", "claude": "Claude Code",
}
FINGERPRINTS = Path(__file__).resolve().parents[2] / "internal/rewrite/assets/agent-fingerprints.json"


def fingerprint_feature(feature, body):
    """Mirror internal/rewrite/fingerprint.go: instructions text or a tool with a parameter."""
    if feature["kind"] == "text":
        return any(feature["text"] in text for _, text in system_fields(body))
    for _, _, tool in declarations(body):
        if tool["name"] != feature["tool"]:
            continue
        if not feature.get("parameter"):
            return True
        schema = next((tool[key] for key in SCHEMA_KEYS if isinstance(tool.get(key), dict)), {})
        if feature["parameter"] in schema.get("properties", {}):
            return True
    return False


def fingerprint_failures(trace):
    """A lane must carry every feature of its own Agent and no other Agent's full fingerprint."""
    family = FAMILIES[trace["release"]["lane"]]
    failures = []
    for scenario in trace["scenarios"]:
        for i, request in enumerate(scenario["requests"]):
            body = request["body"]
            for fingerprint in json.loads(FINGERPRINTS.read_text()):
                hits = [f["id"] for f in fingerprint["features"] if fingerprint_feature(f, body)]
                if fingerprint["family"] == family and len(hits) != len(fingerprint["features"]):
                    missing = [f["id"] for f in fingerprint["features"] if f["id"] not in hits]
                    reason = f"{family} fingerprint feature missing: {', '.join(missing)}"
                elif fingerprint["family"] != family and len(hits) == len(fingerprint["features"]):
                    reason = f"carries the {fingerprint['family']} fingerprint"
                else:
                    continue
                failures.append({"scenario": scenario["scenario"], "round": i + 1, "stage": "fingerprint", "reason": reason})
    return failures


def tool_contract(tool):
    def schema(value):
        if isinstance(value, list):
            return [schema(item) for item in value]
        if not isinstance(value, dict):
            return value
        result = {k: v for k, v in value.items() if k != "description"}
        for key in DESCRIPTION_KEYS & result.keys():
            if isinstance(result[key], dict):
                result[key] = {name: schema(child) for name, child in result[key].items()}
        for key in SUBSCHEMA_KEYS & result.keys():
            result[key] = schema(result[key])
        return result

    result = {key: value for key, value in tool.items() if key not in ("name", "description")}
    for key in SCHEMA_KEYS & result.keys():
        result[key] = schema(result[key])
    return result


def evidence(body):
    def descriptions(value, path):
        if isinstance(value, dict):
            if isinstance(value.get("description"), str):
                yield path + "/description", value["description"]
            for key in sorted(DESCRIPTION_KEYS & value.keys()):
                if isinstance(value[key], dict):
                    for name, child in value[key].items():
                        name = name.replace("~", "~0").replace("/", "~1")
                        yield from descriptions(child, f"{path}/{key}/{name}")
            for key in sorted(SUBSCHEMA_KEYS & value.keys()):
                yield from descriptions(value[key], f"{path}/{key}")
        elif isinstance(value, list):
            for index, child in enumerate(value):
                yield from descriptions(child, f"{path}/{index}")

    fields = dict(system_fields(body))
    for path, namespace, tool in declarations(body):
        fields[path + "/name"] = tool["name"]
        if namespace:
            fields[path.rsplit("/tools/", 1)[0] + "/name"] = namespace
        if isinstance(tool.get("description"), str):
            fields[path + "/description"] = tool["description"]
        for key, value in tool_contract(tool).items():
            if key in SCHEMA_KEYS:
                fields.update(descriptions(tool[key], f"{path}/{key}"))
            fields[f"{path}/{key}"] = json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2)
    return fields


def snippet(text):
    return ESCAPE.sub("", text).strip()[:640]


def changed_fragments(old, new):
    left, right = old.splitlines(), new.splitlines()
    for tag, a, b, c, d in difflib.SequenceMatcher(None, left, right).get_opcodes():
        if tag != "equal":
            yield "\n".join(left[a:b]), "\n".join(right[c:d])


def brand_pattern(lane):
    return re.compile(r"(?<![\w])(?:" + "|".join(map(re.escape, BRANDS[lane])) + r")(?![\w])", re.IGNORECASE)


def rule_candidates(lane, path, new):
    names = BRANDS[lane]
    if path.endswith("/name"):
        if new and any(re.search(r"(?:^|[_-])(?i:" + re.escape(n.replace(" ", "_")) + r")(?=$|[_-]|[A-Z])", new)
                       or n.casefold() == new.casefold() for n in names):
            yield {"kind": "tool-alias", "match": new,
                   "check": "Rewrite the declaration and exact references together, then restore the original executable name."}
        return
    if path.startswith(("/tools/", "/functions/")) and not path.endswith("/description"):
        return
    pattern = brand_pattern(lane)
    for line in new.splitlines():
        if lane == "claude" and line.lstrip().startswith((
            "- If the user asks for help or wants to give feedback",
            "- /help:", "- To give feedback,", "- The most recent Claude models are",
            "- Claude Code is available", "- Fast mode for Claude Code",
        )):
            yield {"kind": "vendor-content-review", "match": line,
                   "check": "Review the exact fragment for removal while retaining adjacent task and tool instructions."}
            continue
        if not pattern.search(line) or len(line) > 2048:
            continue
        # Exact reviewed fragments are safer than enabling a broad new word rule.
        start = SELF.search(line)
        code_like = line.startswith((">", "`", "~~~", "$ ", "    ")) or "`" in line or re.search(r"(?i)\b(?:class|const|interface|function)\s+", line)
        if code_like or re.search(r"(?i)(?:/|\\|\.json\b|--[\w-]|\bopencode run\b)", line):
            yield {"kind": "preserve", "match": line, "expected": line,
                   "check": "Keep the literal command, path or code identifier byte-for-byte."}
        elif start:
            tail = line[start.end():]
            if any(re.match(re.escape(name) + r"(?!\w)", tail, re.IGNORECASE) for name in names):
                literal = re.split(r"(?<=[.!?。])\s+", line[start.start():], maxsplit=1)[0]
                candidate = {"kind": "identity-template", "match": literal,
                             "check": "Review the source sentence and retain any behavior or tool constraints."}
                if start.start() == 0 and not re.search(r"(?i)\b(?:must|do not|never|only|always|should)\b", literal):
                    candidate["proposed_replacement"] = NATIVE_IDENTITY
                yield candidate
        elif lane == "pi" and "PiAgent" in line:
            yield {"kind": "preserve", "match": line, "expected": line,
                   "check": "The occurrence is outside a confirmed self-declaration or executable tool field."}


def unidentified_candidate(lane, path, text):
    # The runner knows the source CLI. A generic self-introduction still needs
    # an additional identifying signal before it can become a runtime rule.
    if not path.startswith(("/tools/", "/functions/")):
        first = text.lstrip().split("\n", 1)[0]
        if lane == "gemini" and re.match(r"(?i)^you are (?:an? )?(?:non-interactive |interactive )?CLI agent\b", first):
            sentence = re.split(r"(?<=[.!?])\s+", first, maxsplit=1)[0]
            return {"kind": "identity-review", "match": sentence,
                    "check": "Require an additional Gemini CLI signal before using this generic identity fragment for recognition."}


def scenario_key(trace, scenario):
    return (trace["release"]["lane"], trace["config_revision"], scenario["scenario"],
            scenario["protocol"], scenario["model"])


def compare(trace, baseline):
    previous = {scenario_key(baseline, s): s for s in baseline["scenarios"]} if baseline else {}
    output = []
    old_version = baseline["release"]["version"] if baseline else None
    for scenario in trace["scenarios"]:
        old = previous.get(scenario_key(trace, scenario))
        if old is None:
            output.append({"scenario": scenario["scenario"], "status": "baseline-created"})
        elif old["status"] != scenario["status"]:
            output.append({"scenario": scenario["scenario"], "status": "capability-changed",
                           "old_version": old_version, "before": old["status"], "after": scenario["status"]})
        before = old["requests"] if old else []
        for index, current in enumerate(scenario["requests"]):
            if old is not None and index >= len(before):
                output.append({"scenario": scenario["scenario"], "round": index + 1, "status": "request-added",
                               "old_version": old_version, "before": "absent", "after": "captured"})
                continue
            current_fields = evidence(current["body"])
            old_fields = evidence(before[index]["body"]) if index < len(before) else {}
            for path in sorted(current_fields.keys() | old_fields.keys()):
                original, modified = old_fields.get(path, ""), current_fields.get(path, "")
                if original == modified:
                    continue
                for left, right in changed_fragments(original, modified):
                    candidates = list(rule_candidates(trace["release"]["lane"], path, right))
                    review = unidentified_candidate(trace["release"]["lane"], path, right)
                    if review:
                        candidates.append(review)
                    # Initial traces still provide evidence for candidates, without calling every field a drift.
                    if old is None and not candidates:
                        continue
                    output.append({
                        "scenario": scenario["scenario"], "round": index + 1, "path": path,
                        "old_version": old_version if old else None,
                        "new_version": trace["release"]["version"], "request_sha256": current["sha256"],
                        "before": snippet(left), "after": snippet(right), "candidates": candidates,
                    })
        if old is not None and len(before) > len(scenario["requests"]):
            output.append({"scenario": scenario["scenario"], "status": "requests-removed", "old_version": old_version,
                           "before": len(before), "after": len(scenario["requests"])})
    return output


class ReplayPlugin(Plugin):
    def __init__(self, library):
        self.audit = []
        super().__init__(library)

    def _host_call(self, ctx, method, request, length, output):
        if method == b"host.log":
            payload = json.loads(ctypes.string_at(request, length))
            self.audit.append(payload.get("fields", {}))
        return super()._host_call(ctx, method, request, length, output)


def encoded(body):
    return base64.b64encode(canonical(body)).decode()


def decoded(result, fallback):
    return json.loads(base64.b64decode(result["Body"])) if result.get("Body") else fallback


def tool_parts(tool):
    args = next(tool[key] for key in ("arguments", "args", "input") if key in tool)
    return tool["name"], tool.get("namespace", ""), json.loads(args) if isinstance(args, str) else args


def checkout_tool(protocol, body):
    if protocol == "chat":
        tool = body["choices"][0]["message"]["tool_calls"][0]["function"]
    elif protocol == "responses":
        tool = body["output"][0]
    elif protocol == "gemini":
        tool = body["candidates"][0]["content"]["parts"][0]["functionCall"]
    else:
        tool = body["content"][0]
    return tool_parts(tool)


def streamed_tool(protocol, wire):
    events = [json.loads(line[6:]) for line in wire.splitlines() if line.startswith(b"data: ") and line[6:] != b"[DONE]"]
    if protocol == "chat":
        tool = {"name": "", "arguments": ""}
        for event in events:
            for choice in event.get("choices", []):
                for call in choice.get("delta", {}).get("tool_calls", []):
                    function = call.get("function", {})
                    for key in ("name", "arguments"):
                        tool[key] += function.get(key, "")
                    if "namespace" in function:
                        tool["namespace"] = function["namespace"]
        return tool_parts(tool)
    if protocol == "responses":
        for event in events:
            if event.get("type") == "response.output_item.done" and event["item"].get("type") == "function_call":
                return tool_parts(event["item"])
    elif protocol == "gemini":
        for event in events:
            for candidate in event.get("candidates", []):
                for part in candidate.get("content", {}).get("parts", []):
                    if "functionCall" in part:
                        return tool_parts(part["functionCall"])
    else:
        block, partial = None, []
        for event in events:
            if event.get("type") == "content_block_start" and event["content_block"].get("type") == "tool_use":
                block = event["content_block"]
            if event.get("type") == "content_block_delta" and event.get("delta", {}).get("type") == "input_json_delta":
                partial.append(event["delta"]["partial_json"])
        if block:
            return tool_parts({**block, "input": "".join(partial) if partial else block["input"]})
    raise ValueError("stream contains no completed tool call")


def round_trip(plugin, rid, model, protocol, original, upstream, streamed):
    metadata = {"RequestID": rid, "SourceFormat": FORMATS[protocol]}
    body, events = response(protocol, model, upstream)
    expected = (original["name"], original.get("namespace", ""), original["arguments"])
    result = decoded(plugin.call("response.intercept_after", {**metadata, "Body": encoded(body)}), body)
    if checkout_tool(protocol, result) != expected:
        raise ValueError("non-streaming tool round trip changed the client name, namespace or arguments")
    if not streamed:
        return
    wire = sse(protocol, events)
    forwarded = bytearray()
    for offset in range(0, len(wire), 29):
        chunk = base64.b64encode(wire[offset:offset + 29]).decode()
        output = plugin.call("response.intercept_stream_chunk", {**metadata, "ChunkIndex": offset, "Body": chunk})
        if not output.get("DropChunk"):
            forwarded += base64.b64decode(output.get("Body") or chunk)
    if b"agent_alias_restore_failed" in forwarded or streamed_tool(protocol, forwarded) != expected:
        raise ValueError("streaming tool round trip changed the client name, namespace or arguments")


def ordinary_messages(body):
    for key in ("messages", "input", "contents"):
        for msg in body.get(key, []) if isinstance(body.get(key), list) else []:
            if msg.get("role") in ("system", "developer", "tool", "function") or msg.get("type", "").endswith(("_call", "_output")):
                continue
            if not isinstance(msg.get("content"), list) and "parts" not in msg:
                yield key, msg.get("role"), msg.get("content")
            else:
                blocks = msg.get("parts", msg.get("content", []))
                yield key, msg.get("role"), [part for part in blocks if "text" in part and part.get("type") not in ("tool_use", "tool_result")]


def replay_request(plugin, lane, scenario, request, rid, first):
    before = request["body"]
    protocol = scenario["protocol"]
    plugin.audit.clear()
    metadata = {"RequestID": rid, "SourceFormat": FORMATS[protocol]}
    try:
        result = plugin.call("request.intercept_after", {
            **metadata, "Stream": True, "Metadata": {"selected_auth_index": "mock"}, "Body": encoded(before),
        })
        if result.get("Terminate"):
            raise ValueError("bridge rejected the captured request")
        after = decoded(result, before)
        counters = copy.deepcopy(plugin.audit[-1]) if plugin.audit else {}
        if list(ordinary_messages(before)) != list(ordinary_messages(after)):
            raise ValueError("bridge edited ordinary conversation text")
        originals = {path: (ns, t) for path, ns, t in declarations(before)}
        rewritten = {path: (ns, t) for path, ns, t in declarations(after)}
        if originals.keys() != rewritten.keys():
            raise ValueError("bridge changed the tool declaration shape")
        aliases = []
        missed_references = []
        fields = evidence(after)
        downstream_text = {path: text for path, text in fields.items() if not path.startswith(("/tools/", "/functions/"))
                           or path.endswith("/description")}
        after_text = "\n".join(downstream_text.values())
        for path, (namespace, tool) in originals.items():
            upstream_ns, upstream = rewritten[path]
            if tool_contract(tool) != tool_contract(upstream):
                raise ValueError("bridge edited the tool parameter contract")
            if (namespace, tool["name"]) == (upstream_ns, upstream["name"]):
                continue
            aliases.append({"path": path, "original": tool["name"], "upstream": upstream["name"],
                            "namespace": namespace, "upstream_namespace": upstream_ns})
            original_id = (namespace + "." if namespace else "") + tool["name"]
            token = r"(?<![\w.-])" + re.escape(original_id) + r"(?![\w.-])"
            if re.search(token, after_text):
                missed_references.append(original_id)
            call = {"name": tool["name"], "arguments": {}}
            upstream_call = {"name": upstream["name"], "arguments": {}}
            if namespace:
                call["namespace"] = namespace
                upstream_call["namespace"] = upstream_ns
            if tool["name"] != "execute":
                round_trip(plugin, rid, scenario["model"], protocol, call, upstream_call, False)
        selected = scenario.get("tool_call") if first else None
        code_aliases = []
        if selected:
            original, upstream = copy.deepcopy(selected), copy.deepcopy(selected)
            path = selected.get("declaration")
            if path in rewritten:
                ns, tool = rewritten[path]
                upstream["name"] = tool["name"]
                if ns:
                    upstream["namespace"] = ns
            if "code" in selected["arguments"]:
                before_catalog = [name for name, _ in catalog_tools(before)]
                after_catalog = [name for name, _ in catalog_tools(after)]
                if len(before_catalog) != len(after_catalog):
                    raise ValueError("bridge changed the Code Mode read catalog shape")
                for old, new in zip(before_catalog, after_catalog):
                    upstream["arguments"]["code"] = upstream["arguments"]["code"].replace(old + "(", new + "(")
                    if old != new:
                        code_aliases.append({"original": old, "upstream": new})
                        original_call = {"name": original["name"], "arguments": {"code": f"return await {old}({{}});"}}
                        upstream_call = {"name": upstream["name"], "arguments": {"code": f"return await {new}({{}});"}}
                        round_trip(plugin, rid, scenario["model"], protocol, original_call, upstream_call, False)
            round_trip(plugin, rid, scenario["model"], protocol, original, upstream, True)
        return {
            "request_sha256": request["sha256"], "agent_families": counters.get("agent_families", []),
            "template_hits": counters.get("template_hits", 0), "residual_fixed": counters.get("residual_fixed", 0),
            "residual_protected": counters.get("residual_protected", 0), "aliases": aliases,
            "code_mode_aliases": code_aliases,
            "remaining_brand_fields": [
                {"path": path, "count": len(matches)}
                for path, text in downstream_text.items() if (matches := brand_pattern(lane).findall(text))
            ],
            "remaining_tool_references_for_review": missed_references, "selected_tool_round_trip": selected is not None,
        }
    finally:
        plugin.call("request.complete", {"RequestID": rid})


def validate(trace):
    release = trace["release"]
    lane = release["lane"]
    if trace["schema"] != 1 or lane not in LANES:
        raise ValueError("unsupported trace manifest")
    if release.get("running_version") != release["version"]:
        raise ValueError("trace has no matching running release")
    expected = set(SCENARIOS) | ({"code-mode"} if lane.startswith("opencode") else set())
    if trace["failures"] or len(expected) != len(trace["scenarios"]) or {s["scenario"] for s in trace["scenarios"]} != expected:
        raise ValueError("trace is missing a required scenario or contains capture failures")
    for scenario in trace["scenarios"]:
        if (scenario["protocol"], scenario["model"]) != LANES[lane]:
            raise ValueError("trace scenario uses an unexpected protocol or model")
        requests = scenario["requests"]
        if not requests or any(hashlib.sha256(canonical(req["body"])).hexdigest() != req["sha256"] for req in requests):
            raise ValueError("trace contains empty requests or an invalid request hash")
        first = requests[0]["body"]
        if not list(system_fields(first)) or not list(declarations(first)):
            raise ValueError("first request has no system instructions or tools")
        if scenario["status"] == "unsupported" and scenario["scenario"] == "code-mode":
            if code_mode(first):
                raise ValueError("claimed unsupported Code Mode is present in the actual request")
            continue
        if scenario["status"] != "captured":
            raise ValueError("trace scenario is not captured")
        if scenario["scenario"] == "code-mode" and not code_mode(first):
            raise ValueError("claimed captured Code Mode is absent from the actual request")
        if scenario["scenario"] != "text" and (
            not scenario["tool_call"] or
            not any(has_fixture_result(scenario["protocol"], req["body"]) for req in requests[1:])
        ):
            raise ValueError("trace lacks a completed tool round trip")


def report(trace, baseline, library):
    validate(trace)
    if baseline is not None:
        if baseline["release"]["lane"] != trace["release"]["lane"]:
            raise ValueError("baseline release lane mismatch")
        if baseline["config_revision"] == trace["config_revision"]:
            validate(baseline)
        else:
            baseline = None
    plugin = ReplayPlugin(library)
    try:
        plugin.provider = "antigravity"
        plugin.configure("agent_alias: true\nantigravity_bridge_system_prompt: false\naudit: true\n")
        replays, failures = [], []
        for index, scenario in enumerate(trace["scenarios"]):
            rounds = []
            for i, request in enumerate(scenario["requests"]):
                try:
                    rounds.append(replay_request(plugin, trace["release"]["lane"], scenario, request, f"trace-{index}-{i}", i == 0))
                except (AssertionError, KeyError, TypeError, ValueError) as exc:
                    failures.append({"scenario": scenario["scenario"], "round": i + 1, "stage": "replay",
                                     "reason": snippet(str(exc)) or type(exc).__name__})
            replays.append({"scenario": scenario["scenario"], "status": scenario["status"], "rounds": rounds})
    finally:
        plugin.close()
    return {
        "release": trace["release"],
        "baseline_version": baseline["release"]["version"] if baseline else None,
        "replays": replays,
        "diff": compare(trace, baseline),
        "failures": failures + fingerprint_failures(trace),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("trace", type=Path)
    parser.add_argument("library", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--baseline", type=Path)
    args = parser.parse_args()
    trace = json.loads(args.trace.read_text())
    baseline = json.loads(args.baseline.read_text()) if args.baseline and args.baseline.exists() else None
    result = report(trace, baseline, args.library.resolve())
    args.output.write_bytes(canonical(result) + b"\n")
    return bool(result["failures"])


if __name__ == "__main__":
    sys.exit(main())
