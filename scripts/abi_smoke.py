#!/usr/bin/env python3
"""Check provider switches and bridge defaults against a compiled library."""
import copy
import json
import sys
from pathlib import Path
from abi_support import NATIVE_IDENTITY, NATIVE_SYSTEM, Plugin
from check_version import metadata


def main():
    version = metadata()["version"]
    plugin = Plugin(Path(sys.argv[1]).resolve())
    body = {"messages": [
        {"role": "system", "content": "You are an AI agent running in OpenCode, a coding agent harness."},
        {"role": "developer", "content": "You are Hermes Agent, built by Nous Research."},
        {"role": "user", "content": "You are Cursor."},
        {"role": "assistant", "content": "You are Cline."},
    ], "tools": [{"name": "OpenCode", "description": "You are Cursor."}]}
    expected = copy.deepcopy(body)
    for message in expected["messages"][:2]:
        message["content"] = NATIVE_IDENTITY
    count = 0
    try:
        for agy, native in [(True, True), (True, False), (False, True), (False, False), (None, None)]:
            extra = "" if agy is None else f"enable_agy: {str(agy).lower()}\nenable_antigravity: {str(native).lower()}\n"
            registration = plugin.configure(extra + "antigravity_bridge_system_prompt: false\n")
            assert registration["metadata"]["Version"] == version
            assert registration["metadata"]["GitHubRepository"] == "https://github.com/yurhett/antigravity-prompt-bridge"
            for provider in ["agy", "antigravity", "openai"]:
                plugin.provider = provider
                wanted = provider == "agy" and agy is not False or provider == "antigravity" and native is not False
                assert plugin.transform(body) == (expected if wanted else body), (provider, agy, native)
                count += 1
        plugin.provider = "antigravity"
        plugin.configure()  # Omitted switch must enable bridging.
        bridged = plugin.transform(body)
        assert bridged["messages"][0]["content"] == NATIVE_SYSTEM
        assert bridged["messages"][2:] == body["messages"][2:]
        assert bridged["tools"] == body["tools"]
        assert plugin.transform(bridged) == bridged
    finally:
        plugin.close()
    print(json.dumps({"passed": True, "version": version, "provider_switch_cases": count, "bridge_defaults_on": True}))


if __name__ == "__main__":
    main()
