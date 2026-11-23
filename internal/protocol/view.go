package protocol

import (
	"fmt"
)

type AgentRequestView struct {
	System  []string
	Tools   []AgentToolDeclaration
	Calls   []AgentToolCall
	Results []AgentToolResult
}
type AgentToolDeclaration struct {
	Name, Namespace, Description string
	Schema                       map[string]any
}
type AgentToolCall struct {
	Object          map[string]any
	ID, ArgumentKey string
}
type AgentToolResult struct {
	Object       map[string]any
	ID, ValueKey string
}

func (c AgentToolCall) Name() string { n, _ := c.Object["name"].(string); return n }

func InspectRequest(root any) AgentRequestView {
	_, declarations := CollectTools(root)
	return InspectRequestWithTools(root, declarations)
}

func InspectRequestWithTools(root any, declarations []ToolDeclaration) AgentRequestView {
	view := AgentRequestView{System: PromptTexts(root)}
	for _, declaration := range declarations {
		if declaration.NamespaceOnly {
			continue
		}
		name, _ := declaration.Object["name"].(string)
		description, _ := declaration.Object["description"].(string)
		var schema map[string]any
		for _, key := range toolSchemaKeys {
			if candidate, ok := declaration.Object[key].(map[string]any); ok {
				schema = candidate
				break
			}
		}
		view.Tools = append(view.Tools, AgentToolDeclaration{
			Name: name, Namespace: declaration.Namespace, Description: description, Schema: schema,
		})
	}
	pending := map[string][]string{}
	sequence := 0
	addCall := func(v any, key, id string) {
		if o, ok := v.(map[string]any); ok {
			if s, _ := o["id"].(string); s != "" {
				id = s
			}
			if s, _ := o["call_id"].(string); s != "" {
				id = s
			}
			if id == "" && key == "args" {
				sequence++
				id = fmt.Sprintf("gemini-anonymous-%d", sequence)
				name, _ := o["name"].(string)
				pending[name] = append(pending[name], id)
			}
			view.Calls = append(view.Calls, AgentToolCall{o, id, key})
		}
	}
	addResult := func(o map[string]any, key, id string) {
		if id == "" && key == "response" {
			name, _ := o["name"].(string)
			if len(pending[name]) > 0 {
				id = pending[name][0]
				pending[name] = pending[name][1:]
			}
		}
		view.Results = append(view.Results, AgentToolResult{o, id, key})
	}
	for o, _ := root.(map[string]any); o != nil; {
		for _, key := range []string{"messages", "input", "contents"} {
			arr, _ := o[key].([]any)
			for _, v := range arr {
				msg, _ := v.(map[string]any)
				if msg == nil {
					continue
				}
				role, _ := msg["role"].(string)
				typ, _ := msg["type"].(string)
				if key == "input" {
					switch typ {
					case "function_call":
						addCall(msg, "arguments", "")
					case "custom_tool_call":
						addCall(msg, "input", "")
					case "function_call_output", "custom_tool_call_output":
						id, _ := msg["call_id"].(string)
						addResult(msg, "output", id)
					}
				}
				if role == "assistant" {
					if f, ok := msg["function_call"].(map[string]any); ok {
						addCall(f, "arguments", "legacy")
					}
					cs, _ := msg["tool_calls"].([]any)
					for _, v := range cs {
						tc, _ := v.(map[string]any)
						id, _ := tc["id"].(string)
						addCall(tc["function"], "arguments", id)
					}
					blocks, _ := msg["content"].([]any)
					for _, v := range blocks {
						b, _ := v.(map[string]any)
						if b["type"] == "tool_use" {
							addCall(b, "input", "")
						}
					}
				}
				if role == "tool" || role == "function" {
					id, _ := msg["tool_call_id"].(string)
					if id == "" && role == "function" {
						id = "legacy"
					}
					addResult(msg, "content", id)
				}
				if role == "user" {
					blocks, _ := msg["content"].([]any)
					for _, v := range blocks {
						b, _ := v.(map[string]any)
						if b["type"] == "tool_result" {
							id, _ := b["tool_use_id"].(string)
							addResult(b, "content", id)
						}
					}
				}
				if key == "contents" {
					parts, _ := msg["parts"].([]any)
					for _, v := range parts {
						part, _ := v.(map[string]any)
						if role == "model" {
							for _, field := range []string{"functionCall", "function_call"} {
								addCall(part[field], "args", "")
							}
						}
						if role == "user" {
							for _, field := range []string{"functionResponse", "function_response"} {
								if f, ok := part[field].(map[string]any); ok {
									id, _ := f["id"].(string)
									addResult(f, "response", id)
								}
							}
						}
					}
				}
			}
		}
		o, _ = o["request"].(map[string]any)
	}
	return view
}
