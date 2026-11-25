#!/usr/bin/env python3
"""Synthetic native ABI regressions for four client tool protocols; no network."""
import base64
import json
import sys
from pathlib import Path
from abi_support import Plugin


def enc(value):
    return base64.b64encode(json.dumps(value).encode()).decode()


def main():
    plugin = Plugin(Path(sys.argv[1]).resolve())
    plugin.provider = "antigravity"
    rows = []
    try:
        plugin.configure("agent_alias: true\nantigravity_bridge_system_prompt: false\n")
        for source in ("gemini", "openai", "openai-response", "claude"):
            rid = "protocol-" + source
            # CPA's after hook carries the executed provider request, regardless
            # of the original client response protocol.
            request = {
                "systemInstruction": {"parts": [{"text": "You are Codex. Use tools.codex.read. Files: /tmp/codex."}]},
                "tools": [{"functionDeclarations": [{"name": "codex_probe", "parameters": {"type": "object", "properties": {"codex_key": {"type": "string"}}}}]}],
                "contents": [{"role": "user", "parts": [{"text": "Codex OpenCode pi cursor /tmp/codex"}]}],
            }
            result = plugin.call("request.intercept_after", {"RequestID": rid, "SourceFormat": source, "Stream": True, "Metadata": {"selected_auth_index": "mock"}, "Body": enc(request)})
            assert not result.get("Terminate"), result
            upstream = json.loads(base64.b64decode(result["Body"]))
            assert upstream["contents"] == request["contents"]
            assert upstream["tools"][0]["functionDeclarations"][0]["name"] == "antigravity_probe"
            args = '{"codex_key":"ok"}'
            if source == "gemini":
                reply = {"candidates": [{"content": {"parts": [{"functionCall": {"name": "antigravity_probe", "args": json.loads(args)}}]}}]}
                events = [reply]
            elif source == "openai":
                reply = {"choices": [{"message": {"tool_calls": [{"id": "keep-codex", "type": "function", "function": {"name": "antigravity_probe", "arguments": args}}]}}]}
                events = [{"choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "id": "keep-codex", "type": "function", "function": {"name": "antigravity_probe", "arguments": args[:8]}}]}, "finish_reason": None}]},
                          {"choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, "function": {"arguments": args[8:]}}]}, "finish_reason": None}]},
                          {"choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}]}]
            elif source == "claude":
                reply = {"content": [{"type": "tool_use", "name": "antigravity_probe", "id": "keep-codex", "input": json.loads(args)}]}
                events = [{"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "name": "antigravity_probe", "id": "keep-codex", "input": {}}},
                          {"type": "content_block_delta", "index": 0, "delta": {"type": "input_json_delta", "partial_json": args}}, {"type": "content_block_stop", "index": 0}, {"type": "message_stop"}]
            else:
                item = {"type": "function_call", "id": "keep-id", "call_id": "keep-codex", "name": "antigravity_probe", "arguments": args}
                reply = {"output": [item]}
                events = [{"type": "response.output_item.added", "item": {**item, "arguments": ""}},
                          {"type": "response.function_call_arguments.delta", "item_id": "keep-id", "delta": args},
                          {"type": "response.function_call_arguments.done", "item_id": "keep-id", "arguments": args},
                          {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": reply}]
            result = plugin.call("response.intercept_after", {"RequestID": rid, "SourceFormat": source, "Body": enc(reply)})
            raw = base64.b64decode(result["Body"]).decode()
            assert "codex_probe" in raw and "codex_key" in raw and "antigravity_probe" not in raw, raw
            wire = "".join("data: " + json.dumps(event) + "\r\n\r\n" for event in events).encode()
            output = bytearray()
            for index in range(0, len(wire), 3):
                result = plugin.call("response.intercept_stream_chunk", {"RequestID": rid, "SourceFormat": source, "ChunkIndex": index, "Body": base64.b64encode(wire[index:index+3]).decode()})
                if not result.get("DropChunk"):
                    output.extend(base64.b64decode(result.get("Body", "")))
            text = output.decode()
            assert "codex_probe" in text and "codex_key" in text and "antigravity_probe" not in text and "agent_alias_restore_failed" not in text, text
            plugin.call("request.complete", {"RequestID": rid})
            rows.append({"protocol": source, "nonstream": True, "fragmented_stream": True, "user_unchanged": True})
        # Identity processing is independent: disable keywords to measure only
        # the alias switch, with a non-identity system sentence.
        plugin.configure("agent_alias: false\nunidentified: legacy\nuse_default_keywords: false\nantigravity_bridge_system_prompt: false\n", heuristic=False)
        body = {"system": "Codex integration", "tools": [{"name": "codex_probe"}]}
        assert plugin.transform(body) == body
    finally:
        plugin.close()
    print(json.dumps({"passed": True, "protocols": rows, "unified_switch": True}))


if __name__ == "__main__":
    main()
