"""Protocol views and deterministic model responses for trace capture and ABI replay."""

import json
import re

FORMATS = {
    "chat": "openai", "responses": "openai-response",
    "gemini": "gemini", "messages": "claude",
}
RESULT_TOKEN = "TRACE_FIXTURE_CONTENT_74BE"
ANSWER = "TRACE_DONE"
CALL_ID = "trace_tool_call"
USAGE = {"input_tokens": 10, "output_tokens": 5}


def system_fields(body):
    for key in ("system", "instructions"):
        if key in body:
            yield from text_fields(body[key], "/" + key)
    for key in ("systemInstruction", "system_instruction"):
        if key in body:
            yield from text_fields(body[key].get("parts", []), "/" + key + "/parts")
    for key in ("messages", "input"):
        messages = body.get(key)
        if isinstance(messages, list):
            for index, msg in enumerate(messages):
                if isinstance(msg, dict) and msg.get("role") in ("system", "developer"):
                    yield from text_fields(msg.get("content"), f"/{key}/{index}/content")


def text_fields(value, path):
    if isinstance(value, str):
        yield path, value
    elif isinstance(value, list):
        for index, part in enumerate(value):
            yield from text_fields(part, f"{path}/{index}")
    elif isinstance(value, dict) and isinstance(value.get("text"), str):
        yield path + "/text", value["text"]


def declarations(body):
    def walk(tools, path, namespace=""):
        for index, tool in enumerate(tools):
            at = f"{path}/{index}"
            for key in ("functionDeclarations", "function_declarations"):
                if key in tool:
                    yield from walk(tool[key], f"{at}/{key}", namespace)
            if tool.get("type") == "namespace":
                yield from walk(tool.get("tools", []), at + "/tools", tool["name"])
                continue
            if isinstance(tool.get("function"), dict):
                tool, at = tool["function"], at + "/function"
            if isinstance(tool.get("name"), str):
                yield at, namespace, tool

    for key in ("tools", "functions"):
        yield from walk(body.get(key, []) or [], "/" + key)


def has_fixture_result(protocol, body):
    if protocol == "chat":
        results = [message.get("content") for message in body.get("messages", []) if message.get("role") == "tool"]
    elif protocol == "responses":
        results = [item.get("output") for item in body.get("input", []) if item.get("type") == "function_call_output"]
    elif protocol == "messages":
        results = [block.get("content") for message in body.get("messages", [])
                   if isinstance(message.get("content"), list)
                   for block in message["content"] if block.get("type") == "tool_result"]
    else:
        results = [function for message in body.get("contents", []) for part in message.get("parts", [])
                   for key in ("functionResponse", "function_response") if (function := part.get(key))]
    return any(RESULT_TOKEN in json.dumps(result) for result in results)


CATALOG = re.compile(r"(?m)^[ \t]*-[ \t]+(tools(?:\.[A-Za-z_$][\w$]*){1,2})[ \t]*\(")
READ_PATH_KEY = re.compile(r"\b(filePath|file_path|path|filename|fileName|file)[ \t]*:[ \t]*string\b")
CATALOG_HEADER = "## Code Mode tools (call only inside `execute`)"


def code_mode(body):
    return any(tool["name"] == "execute" for _, _, tool in declarations(body)) and any(
        CATALOG_HEADER in text for _, text in system_fields(body)
    )


def catalog_tools(body):
    for _, text in system_fields(body):
        _, header, catalog = text.partition(CATALOG_HEADER)
        if not header:
            continue
        end = re.search(r"(?m)^#{1,2} ", catalog)
        if end:
            catalog = catalog[:end.start()]
        for match in CATALOG.finditer(catalog):
            signature = catalog[match.end():].partition("):")[0]
            yield match[1], signature


def trace_tool(body, fixture, mode):
    if mode == "code-mode":
        for path, signature in catalog_tools(body):
            key = READ_PATH_KEY.search(signature)
            if path.rsplit(".", 1)[-1] in ("read", "read_file") and key:
                code = f"return await {path}({{ {key[1]}: {json.dumps(str(fixture))} }});"
                declaration = next(at for at, _, tool in declarations(body) if tool["name"] == "execute")
                return {"name": "execute", "arguments": {"code": code}, "declaration": declaration}
        raise ValueError("Code Mode catalog contains no supported file-read member")

    for path, namespace, tool in declarations(body):
        name = tool["name"]
        if name.lower() not in ("read", "read_file", "readfile", "exec_command", "shell", "shell_command"):
            continue
        schema = next((tool[key] for key in ("parameters", "input_schema", "parametersJsonSchema", "parameters_json_schema") if key in tool), {})
        props = schema.get("properties", {})
        if name.lower() in ("read", "read_file", "readfile"):
            key = next((key for key in ("filePath", "file_path", "path", "filename", "fileName", "file") if key in props), None)
            if not key:
                continue
            arguments = {key: str(fixture)}
        elif "cmd" in props or "command" in props:
            key = "cmd" if "cmd" in props else "command"
            command = ["cat", fixture.name] if props[key].get("type") == "array" else f"cat {fixture.name}"
            arguments = {key: command}
            for dir_key in ("workdir", "work_dir", "cwd"):
                if dir_key in props:
                    arguments[dir_key] = str(fixture.parent)
        else:
            continue
        if set(schema.get("required", [])) - arguments.keys():
            continue
        call = {"name": name, "arguments": arguments, "declaration": path}
        if namespace:
            call["namespace"] = namespace
        return call
    raise ValueError("no supported file-read tool in the actual request")


def response(protocol, model, call=None):
    """Return a JSON response and its matching SSE event sequence."""
    name = call["name"] if call else None
    arguments = call["arguments"] if call else None
    args_json = json.dumps(arguments, separators=(",", ":"))
    if protocol == "gemini":
        part = {"functionCall": {"name": name, "args": arguments, "id": CALL_ID}} if call else {"text": ANSWER}
        result = {
            "candidates": [{"content": {"role": "model", "parts": [part]}, "finishReason": "STOP", "index": 0}],
            "usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
        }
        return result, [result]
    if protocol == "chat":
        metadata = {"id": "chatcmpl-trace", "created": 1, "model": model}
        message = {"role": "assistant", "content": ANSWER}
        if call:
            message = {"role": "assistant", "content": None, "tool_calls": [
                {"id": CALL_ID, "type": "function", "function": {"name": name, "arguments": args_json}},
            ]}
        finish = "tool_calls" if call else "stop"
        result = {**metadata, "object": "chat.completion", "choices": [{"index": 0, "message": message, "finish_reason": finish}],
                  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
        delta = dict(message)
        if call:
            delta["tool_calls"] = [{"index": 0, **message["tool_calls"][0]}]
        chunks = ({"delta": delta, "finish_reason": None}, {"delta": {}, "finish_reason": finish})
        events = [{**metadata, "object": "chat.completion.chunk", "choices": [{"index": 0, **chunk}]} for chunk in chunks]
        events.append({**metadata, "object": "chat.completion.chunk", "choices": [], "usage": result["usage"]})
        return result, [*events, None]
    if protocol == "messages":
        block = {"type": "tool_use", "id": CALL_ID, "name": name, "input": arguments} if call else {"type": "text", "text": ANSWER}
        finish = "tool_use" if call else "end_turn"
        result = {
            "id": "msg_trace", "type": "message", "role": "assistant", "model": model,
            "content": [block], "stop_reason": finish, "stop_sequence": None, "usage": USAGE,
        }
        events = [
            {"type": "message_start", "message": {**result, "content": [], "stop_reason": None, "usage": {**USAGE, "output_tokens": 0}}},
            {"type": "content_block_start", "index": 0, "content_block": {**block, **({"input": {}} if call else {"text": ""})}},
            {"type": "content_block_delta", "index": 0, "delta":
                {"type": "input_json_delta", "partial_json": args_json} if call else {"type": "text_delta", "text": ANSWER}},
            {"type": "content_block_stop", "index": 0},
            {"type": "message_delta", "delta": {"stop_reason": finish, "stop_sequence": None}, "usage": {"output_tokens": 5}},
            {"type": "message_stop"},
        ]
        return result, events
    if protocol != "responses":
        raise ValueError(f"unsupported protocol {protocol}")

    part = {"type": "output_text", "text": ANSWER, "annotations": [], "logprobs": []}
    item = {"type": "message", "id": "msg_trace", "status": "completed", "role": "assistant", "content": [part]}
    if call:
        item = {"type": "function_call", "id": "fc_trace", "status": "completed", "call_id": CALL_ID, "name": name, "arguments": args_json}
        if "namespace" in call:
            item["namespace"] = call["namespace"]
    result = {
        "id": "resp_trace", "object": "response", "created_at": 1, "status": "completed", "model": model,
        "output": [item], "error": None, "incomplete_details": None, "parallel_tool_calls": False,
        "usage": {**USAGE, "total_tokens": 15, "input_tokens_details": {"cached_tokens": 0}, "output_tokens_details": {"reasoning_tokens": 0}},
    }
    events = [
        {"type": "response.created", "response": {**result, "status": "in_progress", "output": [], "usage": None}},
        {"type": "response.output_item.added", "output_index": 0,
         "item": {**item, "status": "in_progress", **({"arguments": ""} if call else {"content": []})}},
    ]
    fields = {"item_id": item["id"], "output_index": 0}
    if call:
        events += [
            {"type": "response.function_call_arguments.delta", **fields, "delta": args_json},
            {"type": "response.function_call_arguments.done", **fields, "arguments": args_json},
        ]
    else:
        fields["content_index"] = 0
        events += [
            {"type": "response.content_part.added", **fields, "part": {**part, "text": ""}},
            {"type": "response.output_text.delta", **fields, "delta": ANSWER, "logprobs": []},
            {"type": "response.output_text.done", **fields, "text": ANSWER, "logprobs": []},
            {"type": "response.content_part.done", **fields, "part": part},
        ]
    events += [
        {"type": "response.output_item.done", "output_index": 0, "item": item},
        {"type": "response.completed", "response": result},
    ]
    return result, [dict(event, sequence_number=i) for i, event in enumerate(events)]


def sse(protocol, events):
    chunks = []
    for event in events:
        if event is None:
            chunks.append("data: [DONE]\n\n")
            continue
        prefix = f"event: {event['type']}\n" if protocol in ("messages", "responses") else ""
        chunks.append(prefix + "data: " + json.dumps(event) + "\n\n")
    return "".join(chunks).encode()
