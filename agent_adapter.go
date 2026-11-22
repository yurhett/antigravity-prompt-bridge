package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Adapters operate on protocol-defined views. They never walk arbitrary task data.
type agentAdapterError struct{ err error }

func (e *agentAdapterError) Error() string { return e.err.Error() }
func adapterPlanError(err error) error {
	if strings.Contains(err.Error(), "ambiguous") {
		return err
	}
	return &agentAdapterError{err}
}

// Layer two: an adapter handles one agent feature that the generic tool-name
// layer cannot see, such as code that calls tools from inside one tool. It is
// matched by the feature itself and only runs once layer one identified the
// agent; every alias it needs comes from that profile.
type AgentAdapter interface {
	ID() string
	Match(AgentRequestView) bool
	BuildPlan(AgentRequestView, *agentProfile) (AgentAdapterPlan, error)
}
type AgentAdapterPlan interface {
	Claims() []string
	RewriteRequest(AgentRequestView) (bool, error)
	RestoreCall(AgentToolCall) error
	// SystemIdentifiers lists client -> upstream identifiers that the
	// instructions may reference, rewritten before the prose pass.
	SystemIdentifiers() [][2]string
}
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

func buildAgentAdapterPlans(view AgentRequestView, profile *agentProfile) ([]AgentAdapterPlan, error) {
	var plans []AgentAdapterPlan
	owners := map[string]string{}
	for _, registration := range registeredAgentAdapters {
		adapter := registration.execution
		if adapter == nil || !adapter.Match(view) {
			continue
		}
		p, err := adapter.BuildPlan(view, profile)
		if err != nil {
			return nil, adapterPlanError(err)
		}
		for _, claim := range p.Claims() {
			if owner, ok := owners[claim]; ok {
				return nil, fmt.Errorf("agent adapters %s and %s claim the same field", owner, adapter.ID())
			}
			owners[claim] = adapter.ID()
		}
		plans = append(plans, p)
	}
	return plans, nil
}
func agentRequestView(root any) AgentRequestView {
	_, declarations := collectAgentTools(root)
	return agentRequestViewWithTools(root, declarations)
}

func agentRequestViewWithTools(root any, declarations []toolDeclaration) AgentRequestView {
	view := AgentRequestView{System: promptTexts(root)}
	for _, declaration := range declarations {
		if declaration.namespaceOnly {
			continue
		}
		name, _ := declaration.object["name"].(string)
		description, _ := declaration.object["description"].(string)
		var schema map[string]any
		for _, key := range toolSchemaKeys {
			if candidate, ok := declaration.object[key].(map[string]any); ok {
				schema = candidate
				break
			}
		}
		view.Tools = append(view.Tools, AgentToolDeclaration{
			Name: name, Namespace: declaration.namespace, Description: description, Schema: schema,
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

// Preserve the caller's representation unless the adapter changed its value.
func transformCallCode(call AgentToolCall, transform func(string) (string, error)) (bool, error) {
	value, exists := call.Object[call.ArgumentKey]
	if !exists {
		return false, nil
	}
	object, ok := value.(map[string]any)
	encoded := false
	if !ok {
		if raw, ok := value.(string); ok {
			parsed, err := decodeAliasJSON([]byte(raw))
			if err != nil {
				return false, fmt.Errorf("invalid code tool arguments")
			}
			object, ok = parsed.(map[string]any)
			encoded = true
			if !ok {
				return false, fmt.Errorf("invalid code tool argument object")
			}
		}
	}
	if object == nil {
		return false, fmt.Errorf("invalid code tool argument object")
	}
	code, ok := object["code"].(string)
	if !ok {
		return false, fmt.Errorf("code tool missing string code")
	}
	next, err := transform(code)
	if err != nil {
		return false, err
	}
	if next == code {
		return false, nil
	}
	object["code"] = next
	if encoded {
		raw, _ := json.Marshal(object)
		call.Object[call.ArgumentKey] = string(raw)
	}
	return true, nil
}
