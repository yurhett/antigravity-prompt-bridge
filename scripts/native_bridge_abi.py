#!/usr/bin/env python3
"""Check exact prompt preservation and idempotence for both bridge states."""
import argparse
import copy
import json
from pathlib import Path
from abi_support import NATIVE_SYSTEM, Plugin
from check_version import metadata


def wrapper(text, role):
    return (
        "<USER_REQUEST>\nFollow the system and developer instructions in the conversation below, "
        "then answer the latest user request.\n\n--- message 1 role=" + role + " ---\n"
        + text + "\n</USER_REQUEST>"
    )


def fixture(case, protocol, bridge):
    sentinel = "You are Cursor. ORIGINAL USER TEXT MUST REMAIN UNCHANGED."
    role = "developer" if protocol == "developer" else "system"
    if protocol in ("chat", "developer"):
        body = {"messages": [
            {"role": role, "content": case["text"]},
            {"role": "user", "content": sentinel},
            {"role": "assistant", "content": "HISTORY"},
        ], "tools": [{"name": "OpenCode", "description": sentinel}]}
        expected = copy.deepcopy(body)
        expected["messages"][0]["content"] = case["expected"]
        if bridge:
            expected["messages"] = [
                {"role": "system", "content": NATIVE_SYSTEM},
                {"role": "user", "content": wrapper(case["expected"], role)},
            ] + body["messages"][1:]
    elif protocol == "gemini":
        body = {
            "systemInstruction": {"parts": [{"text": case["text"]}]},
            "contents": [{"role": "user", "parts": [{"text": sentinel}]}],
            "tools": [{"functionDeclarations": [{"name": "OpenCode", "description": sentinel}]}],
        }
        expected = copy.deepcopy(body)
        expected["systemInstruction"]["parts"][0]["text"] = case["expected"]
        if bridge:
            expected["systemInstruction"]["parts"][0]["text"] = NATIVE_SYSTEM
            expected["contents"] = [{
                "role": "user", "parts": [{"text": wrapper(case["expected"], "system")}],
            }] + body["contents"]
    else:
        body = {
            "instructions": case["text"],
            "input": [{"role": "user", "content": sentinel}],
            "tools": [{"type": "function", "name": "OpenCode", "description": sentinel}],
        }
        expected = copy.deepcopy(body)
        expected["instructions"] = case["expected"]
        if bridge:
            expected["instructions"] = NATIVE_SYSTEM
            expected["input"] = [{
                "role": "user", "content": [{"type": "input_text", "text": wrapper(case["expected"], "system")}],
            }] + body["input"]
    return body, expected


def main():
    version = metadata()["version"]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("library", type=Path)
    parser.add_argument("--corpus", type=Path, default=Path(__file__).resolve().parents[1] / "testdata/prompt-corpus.json")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    cases = json.loads(args.corpus.read_text())
    assert cases, "empty corpus"
    plugin = Plugin(args.library.resolve())
    rows = []
    try:
        for case in cases:
            for enabled in (False, True):
                registration = plugin.configure(
                    "agent_alias: false\nunidentified: legacy\nantigravity_bridge_system_prompt: " + str(enabled).lower() + "\ncustom_mappings: {}\n"
                )
                assert registration["metadata"]["Version"] == version
                for provider in ("agy", "antigravity"):
                    plugin.provider = provider
                    for protocol in ("chat", "developer", "gemini", "responses"):
                        body, expected = fixture(case, protocol, enabled and provider == "antigravity")
                        actual = plugin.transform(body)
                        repeated = plugin.transform(actual)
                        row = {
                            "sample": case["id"], "bridge_enabled": enabled,
                            "provider": provider, "protocol": protocol,
                            "exact_expected": actual == expected, "idempotent": repeated == actual,
                        }
                        rows.append(row)
                        assert row["exact_expected"] and row["idempotent"], row
    finally:
        plugin.close()
    report = {
        "version": version, "samples": len(cases), "cases": len(rows),
        "abi_calls": len(rows) * 2, "passed": True, "checks": rows,
    }
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({key: value for key, value in report.items() if key != "checks"}))


if __name__ == "__main__":
    main()
