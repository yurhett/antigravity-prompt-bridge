package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"sort"
	"strings"
)

type streamAliasCall struct {
	function         map[string]any
	arguments        strings.Builder
	firstDelta       map[string]any
	chatItem         map[string]any
	chatFunction     map[string]any
	choice, index    int
	legacy, freeform bool
}

func aliasStreamFormatSupported(format string) bool {
	switch format {
	case "gemini", "openai", "openai-response", "anthropic", "claude":
		return true
	}
	return false
}
func aliasNumber(v any) int { n, _ := v.(json.Number); i, _ := n.Int64(); return int(i) }
func (s *agentAliasState) newStreamCall(key string, c *streamAliasCall) error {
	if _, ok := s.calls[key]; ok {
		return fmt.Errorf("duplicate tool stream start")
	}
	if len(s.calls) >= maxAliasRequests {
		return fmt.Errorf("too many tool calls")
	}
	s.calls[key] = c
	return nil
}
func (s *agentAliasState) appendArguments(c *streamAliasCall, text string) error {
	s.pendingBytes += len(text)
	if s.pendingBytes+len(s.stream) > maxAliasStreamBytes {
		return fmt.Errorf("tool argument buffer limit")
	}
	c.arguments.WriteString(text)
	return nil
}
func (s *agentAliasState) finishStreamCall(key string) {
	if c := s.calls[key]; c != nil {
		s.pendingBytes -= c.arguments.Len()
		delete(s.calls, key)
	}
}
func (s *agentAliasState) restoredStreamArguments(c *streamAliasCall, text string) (string, error) {
	f := map[string]any{"name": c.function["name"]}
	if namespace, ok := c.function["namespace"]; ok {
		f["namespace"] = namespace
	}
	field := "arguments"
	if c.freeform {
		field = "input"
		f["type"] = "custom_tool_call"
	}
	f[field] = text
	if err := s.restoreFunction(f); err != nil {
		return "", err
	}
	return f[field].(string), nil
}

// Only incomplete tool arguments wait. Normal text events are forwarded as they
// arrive. Each native protocol supplies a per-call completion boundary.
func (s *agentAliasState) streamEvent(event map[string]any) ([]map[string]any, bool, error) {
	var before []map[string]any
	kind, _ := event["type"].(string)
	if choices, ok := event["choices"].([]any); ok {
		for _, v := range choices {
			choice, ok := v.(map[string]any)
			if !ok {
				continue
			}
			ci := aliasNumber(choice["index"])
			delta, _ := choice["delta"].(map[string]any)
			calls, _ := delta["tool_calls"].([]any)
			if f, ok := delta["function_call"].(map[string]any); ok {
				calls = append(calls, map[string]any{"index": json.Number("-1"), "function": f})
			}
			for _, v := range calls {
				item, ok := v.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("invalid chat tool delta")
				}
				idx := aliasNumber(item["index"])
				key := fmt.Sprintf("chat:%d:%d", ci, idx)
				c := s.calls[key]
				if c == nil {
					c = &streamAliasCall{function: map[string]any{}, chatItem: map[string]any{}, chatFunction: map[string]any{}, choice: ci, index: idx, legacy: idx == -1}
					if err := s.newStreamCall(key, c); err != nil {
						return nil, false, err
					}
				}
				f, _ := item["function"].(map[string]any)
				for k, v := range item {
					if k != "function" {
						c.function[k] = v
						c.chatItem[k] = v
					}
				}
				for k, v := range f {
					switch k {
					case "name":
						part, _ := v.(string)
						name, _ := c.function["name"].(string)
						c.function["name"] = name + part
					case "arguments":
						part, ok := v.(string)
						if !ok {
							return nil, false, fmt.Errorf("invalid argument fragment")
						}
						if err := s.appendArguments(c, part); err != nil {
							return nil, false, err
						}
					default:
						c.function[k] = v
						c.chatFunction[k] = v
					}
				}
			}
			delete(delta, "tool_calls")
			delete(delta, "function_call")
			if choice["finish_reason"] != nil {
				var keys []string
				for key, c := range s.calls {
					if strings.HasPrefix(key, "chat:") && c.choice == ci {
						keys = append(keys, key)
					}
				}
				sort.Strings(keys)
				for _, key := range keys {
					c := s.calls[key]
					args, err := s.restoredStreamArguments(c, c.arguments.String())
					if err != nil {
						return nil, false, err
					}
					identity := map[string]any{"name": c.function["name"]}
					if namespace, ok := c.function["namespace"]; ok {
						identity["namespace"] = namespace
					}
					if err := s.restoreFunction(identity); err != nil {
						return nil, false, err
					}
					f := c.chatFunction
					f["name"], f["arguments"] = identity["name"], args
					if namespace, ok := identity["namespace"]; ok {
						f["namespace"] = namespace
					}
					tool := c.chatItem
					tool["index"], tool["function"] = c.index, f
					if _, ok := tool["type"]; !ok {
						tool["type"] = "function"
					}
					if id, ok := c.function["id"]; ok {
						tool["id"] = id
					}
					d := map[string]any{"tool_calls": []any{tool}}
					if c.legacy {
						d = map[string]any{"function_call": f}
					}
					chunk := map[string]any{}
					for k, v := range event {
						if k != "choices" && k != "usage" {
							chunk[k] = v
						}
					}
					chunk["choices"] = []any{map[string]any{"index": ci, "delta": d, "finish_reason": nil}}
					before = append(before, chunk)
					s.finishStreamCall(key)
				}
			}
		}
		return before, true, nil
	}
	if kind == "content_block_start" {
		block, _ := event["content_block"].(map[string]any)
		if block["type"] == "tool_use" {
			key := fmt.Sprintf("anthropic:%d", aliasNumber(event["index"]))
			c := &streamAliasCall{function: map[string]any{"name": block["name"]}}
			if err := s.newStreamCall(key, c); err != nil {
				return nil, false, err
			}
			target := block
			if input, ok := block["input"].(map[string]any); ok && len(input) == 0 {
				target = map[string]any{"name": block["name"]}
				if ns, ok := block["namespace"]; ok {
					target["namespace"] = ns
				}
			}
			if err := s.restoreFunction(target); err != nil {
				return nil, false, err
			}
			block["name"] = target["name"]
			if ns, ok := target["namespace"]; ok {
				block["namespace"] = ns
			}
		}
		return nil, true, nil
	}
	if kind == "content_block_delta" {
		key := fmt.Sprintf("anthropic:%d", aliasNumber(event["index"]))
		delta, _ := event["delta"].(map[string]any)
		if delta["type"] == "input_json_delta" {
			c := s.calls[key]
			if c == nil {
				return nil, false, fmt.Errorf("argument delta without tool start")
			}
			text, ok := delta["partial_json"].(string)
			if !ok {
				return nil, false, fmt.Errorf("invalid tool argument delta")
			}
			if err := s.appendArguments(c, text); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
		return nil, true, nil
	}
	if kind == "content_block_stop" {
		key := fmt.Sprintf("anthropic:%d", aliasNumber(event["index"]))
		if c := s.calls[key]; c != nil {
			if c.arguments.Len() > 0 {
				text, err := s.restoredStreamArguments(c, c.arguments.String())
				if err != nil {
					return nil, false, err
				}
				before = append(before, map[string]any{"type": "content_block_delta", "index": event["index"], "delta": map[string]any{"type": "input_json_delta", "partial_json": text}})
			}
			s.finishStreamCall(key)
		}
		return before, true, nil
	}
	if kind == "response.output_item.added" {
		item, _ := event["item"].(map[string]any)
		if item["type"] == "function_call" || item["type"] == "custom_tool_call" {
			id, _ := item["id"].(string)
			name, _ := item["name"].(string)
			if id == "" || name == "" {
				return nil, false, fmt.Errorf("tool stream missing item id or name")
			}
			c := &streamAliasCall{function: map[string]any{"name": name}, freeform: item["type"] == "custom_tool_call"}
			if namespace, ok := item["namespace"]; ok {
				c.function["namespace"] = namespace
			}
			if err := s.newStreamCall("responses:"+id, c); err != nil {
				return nil, false, err
			}
			identity := map[string]any{"name": name}
			if namespace, ok := item["namespace"]; ok {
				identity["namespace"] = namespace
			}
			if err := s.restoreFunction(identity); err != nil {
				return nil, false, err
			}
			item["name"] = identity["name"]
			if namespace, ok := identity["namespace"]; ok {
				item["namespace"] = namespace
			}
			field := "arguments"
			if c.freeform {
				field = "input"
			}
			if text, ok := item[field].(string); ok && text != "" {
				restored, err := s.restoredStreamArguments(c, text)
				if err != nil {
					return nil, false, err
				}
				item[field] = restored
			}
		}
		return nil, true, nil
	}
	if kind == "response.function_call_arguments.delta" || kind == "response.custom_tool_call_input.delta" {
		id, _ := event["item_id"].(string)
		c := s.calls["responses:"+id]
		if c == nil {
			return nil, false, fmt.Errorf("argument delta without tool item")
		}
		text, ok := event["delta"].(string)
		if !ok {
			return nil, false, fmt.Errorf("invalid response tool delta")
		}
		if c.firstDelta == nil {
			c.firstDelta = event
		}
		if err := s.appendArguments(c, text); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	if kind == "response.function_call_arguments.done" || kind == "response.custom_tool_call_input.done" {
		id, _ := event["item_id"].(string)
		c := s.calls["responses:"+id]
		if c == nil {
			return nil, false, fmt.Errorf("tool completion without item")
		}
		field := "arguments"
		if c.freeform {
			field = "input"
		}
		text, ok := event[field].(string)
		if !ok {
			return nil, false, fmt.Errorf("missing complete tool arguments")
		}
		if c.arguments.Len() > 0 && text != c.arguments.String() {
			return nil, false, fmt.Errorf("tool deltas disagree with completion")
		}
		restored, err := s.restoredStreamArguments(c, text)
		if err != nil {
			return nil, false, err
		}
		event[field] = restored
		if c.firstDelta != nil {
			c.firstDelta["delta"] = restored
			before = append(before, c.firstDelta)
			c.firstDelta = nil
		}
		s.pendingBytes -= c.arguments.Len()
		c.arguments.Reset()
		return before, true, nil
	}
	if kind == "response.output_item.done" {
		item, _ := event["item"].(map[string]any)
		id, _ := item["id"].(string)
		key := "responses:" + id
		if c := s.calls[key]; c != nil {
			if c.firstDelta != nil {
				field := "arguments"
				if c.freeform {
					field = "input"
				}
				text, ok := item[field].(string)
				if !ok || text != c.arguments.String() {
					return nil, false, fmt.Errorf("incomplete tool arguments")
				}
				restored, err := s.restoredStreamArguments(c, text)
				if err != nil {
					return nil, false, err
				}
				c.firstDelta["delta"] = restored
				before = append(before, c.firstDelta)
			}
			s.finishStreamCall(key)
		}
	}
	if kind == "message_stop" || kind == "response.completed" || kind == "response.failed" || kind == "response.incomplete" {
		if len(s.calls) > 0 {
			return nil, false, fmt.Errorf("stream ended with unfinished tool calls")
		}
	}
	if err := s.restoreResponse(event); err != nil {
		return nil, false, err
	}
	return before, true, nil
}

func (s *agentAliasState) encodeStreamEvent(event map[string]any, template [][]byte) []byte {
	if _, ok := event["sequence_number"]; ok {
		event["sequence_number"] = s.sequence
		s.sequence++
	}
	raw, _ := json.Marshal(event)
	var out bytes.Buffer
	inserted := false
	for _, line := range template {
		if bytes.HasPrefix(line, []byte("data:")) {
			if !inserted {
				out.WriteString("data: ")
				out.Write(raw)
				out.WriteByte('\n')
				inserted = true
			}
		} else {
			out.Write(line)
			out.WriteByte('\n')
		}
	}
	if !inserted {
		if kind, ok := event["type"].(string); ok {
			out.WriteString("event: " + kind + "\n")
		}
		out.WriteString("data: ")
		out.Write(raw)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return out.Bytes()
}
func handleAliasStream(request []byte) []byte {
	var req pluginapi.StreamChunkInterceptRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return mustErrorEnvelope("invalid_request", err.Error())
	}
	s := agentAliases.get(req.RequestID)
	if s == nil || req.ChunkIndex < 0 || len(req.Body) == 0 {
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{DropChunk: true})
	}
	fail := func() []byte {
		s.failed = true
		s.stream = nil
		s.calls = map[string]*streamAliasCall{}
		s.pendingBytes = 0
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{Body: append(append([]byte("event: error\ndata: "), aliasErrorBody()...), []byte("\n\n")...)})
	}
	s.stream = append(s.stream, req.Body...)
	if len(s.stream)+s.pendingBytes > maxAliasStreamBytes {
		return fail()
	}
	trimmed := bytes.TrimSpace(s.stream)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		value, err := decodeAliasJSON(trimmed)
		if err != nil {
			return mustEnvelope(pluginapi.StreamChunkInterceptResponse{DropChunk: true})
		}
		if err = s.restoreResponse(value); err != nil {
			return fail()
		}
		body, _ := json.Marshal(value)
		s.stream = nil
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{Body: body})
	}
	s.stream = bytes.ReplaceAll(s.stream, []byte("\r\n"), []byte("\n"))
	var output bytes.Buffer
	for {
		end := bytes.Index(s.stream, []byte("\n\n"))
		if end < 0 {
			break
		}
		frame := s.stream[:end]
		s.stream = s.stream[end+2:]
		lines := bytes.Split(frame, []byte("\n"))
		var data [][]byte
		for _, line := range lines {
			if bytes.HasPrefix(line, []byte("data:")) {
				data = append(data, bytes.TrimPrefix(line[len("data:"):], []byte(" ")))
			}
		}
		if len(data) == 0 {
			output.Write(frame)
			output.WriteString("\n\n")
			continue
		}
		payload := bytes.Join(data, []byte("\n"))
		if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			if len(s.calls) > 0 {
				return fail()
			}
			output.Write(frame)
			output.WriteString("\n\n")
			continue
		}
		value, err := decodeAliasJSON(payload)
		if err != nil {
			return fail()
		}
		event, ok := value.(map[string]any)
		if !ok {
			return fail()
		}
		before, emit, err := s.streamEvent(event)
		if err != nil {
			return fail()
		}
		for _, extra := range before {
			output.Write(s.encodeStreamEvent(extra, nil))
		}
		if emit {
			output.Write(s.encodeStreamEvent(event, lines))
		}
	}
	if output.Len() == 0 {
		return mustEnvelope(pluginapi.StreamChunkInterceptResponse{DropChunk: true})
	}
	return mustEnvelope(pluginapi.StreamChunkInterceptResponse{Body: output.Bytes()})
}
