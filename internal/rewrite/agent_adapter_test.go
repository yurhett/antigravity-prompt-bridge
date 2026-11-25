package rewrite

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

const codeModeSystem = "You are an AI agent running in OpenCode, a coding agent harness.\n# Code Mode\nThe tools listed below, and `search`, cannot be called directly: they only work as JavaScript inside the `execute` tool.\n## Code Mode tools (call only inside `execute`)\n- opencode (3 tools, 3 shown)\n  - tools.opencode.worktree_list(): Promise<{}>\n  - tools.opencode.session_move({ directory: string }): Promise<{}>\n  - tools.opencode.codex_probe({ value: string }): Promise<{}>\n"

func codeModeTool() map[string]any {
	return map[string]any{"name": "execute", "description": "Run JavaScript in a confined Code Mode runtime to script tool calls.", "parametersJsonSchema": map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}}, "required": []any{"code"}}}
}
func codeModeRoot() map[string]any {
	return map[string]any{"system": codeModeSystem, "tools": []any{map[string]any{"functionDeclarations": []any{codeModeTool()}}}}
}
func mustCodePlan(t *testing.T) *openCodePlan {
	t.Helper()
	root := codeModeRoot()
	p, e := (openCodeAdapter{}).BuildPlan(protocol.InspectRequest(root), defaultBrandCatalog.identify(protocol.PromptTexts(root)))
	if e != nil {
		t.Fatal(e)
	}
	return p.(*openCodePlan)
}
func TestOpenCodeJavaScriptPreciseRoundTrip(t *testing.T) {
	p := mustCodePlan(t)
	for _, sample := range []struct{ original, upstream string }{
		{`return await tools.opencode.worktree_list();`, `return await tools.antigravity.worktree_list();`},
		{`return tools["opencode"]["session_move"]({content:"OpenCode tools.opencode.session_move",path:"/tmp/opencode"});`, `return tools["antigravity"]["session_move"]({content:"OpenCode tools.opencode.session_move",path:"/tmp/opencode"});`},
		{`return tools?.opencode?.codex_probe({value:"Codex"});`, `return tools?.antigravity?.antigravity_probe({value:"Codex"});`},
		{`// tools.opencode.worktree_list\nconst x=/tools.opencode/; return "tools.opencode.worktree_list()";`, `// tools.opencode.worktree_list\nconst x=/tools.opencode/; return "tools.opencode.worktree_list()";`},
		{"return await Promise.allSettled([tools.opencode.worktree_list(),search({namespace:'opencode',query:'opencode'})]);", "return await Promise.allSettled([tools.antigravity.worktree_list(),search({namespace:\"antigravity\",query:'opencode'})]);"},
		{`function f(tools){return tools.opencode.worktree_list()} return tools.opencode.worktree_list();`, `function f(tools){return tools.opencode.worktree_list()} return tools.antigravity.worktree_list();`},
		{`{ let tools={}; tools.opencode.x(); } return tools.opencode.worktree_list();`, `{ let tools={}; tools.opencode.x(); } return tools.antigravity.worktree_list();`},
		{`function f(){return tools.opencode.x(); var tools={};} return tools.opencode.worktree_list();`, `function f(){return tools.opencode.x(); var tools={};} return tools.antigravity.worktree_list();`},
		{`const f=({tools})=>tools.opencode.x(); return tools.opencode.worktree_list();`, `const f=({tools})=>tools.opencode.x(); return tools.antigravity.worktree_list();`},
		{`try{}catch(tools){tools.opencode.x()} return tools.opencode.worktree_list();`, `try{}catch(tools){tools.opencode.x()} return tools.antigravity.worktree_list();`},
		{`function f(search){return search({namespace:"opencode"})}; return search({namespace:"opencode"});`, `function f(search){return search({namespace:"opencode"})}; return search({namespace:"antigravity"});`},
	} {
		a, e := parseAgentJavaScript(sample.original)
		if e != nil {
			t.Fatal(e, sample.original)
		}
		got, e := a.rewrite(p, false)
		if e != nil || got != sample.upstream {
			t.Fatalf("rewrite %q: %q %v", sample.original, got, e)
		}
		a, e = parseAgentJavaScript(got)
		if e != nil {
			t.Fatal(e)
		}
		restored, e := a.rewrite(p, true)
		if e != nil {
			t.Fatal(e)
		}
		// Quote style of semantic namespace string literals can canonicalize; every other source span must stay exact.
		expected := strings.ReplaceAll(sample.original, "namespace:'opencode'", "namespace:\"opencode\"")
		if restored != expected {
			t.Fatalf("restore: %q want %q", restored, expected)
		}
	}
	for _, code := range []string{`return tools[ns].foo()`, `return search({namespace:ns})`, `return search(options)`, `return eval("tools.antigravity.foo()")`} {
		a, e := parseAgentJavaScript(code)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.rewrite(p, true); e == nil {
			t.Fatal("dynamic execution accepted", code)
		}
	}
}
func TestOpenCodeAdapterDetectionAndSystemReferences(t *testing.T) {
	root := codeModeRoot()
	root["system"] = codeModeSystem + "Other rules: tools.opencode.session_move. Use `tools['opencode'][\"worktree_list\"]()`. /tmp/opencode opencode.json https://opencode.ai plugins/opencode.so"
	raw, _ := json.Marshal(root)
	body, state, e := aliasAgentRequest(raw)
	if e != nil || len(state.adapters) != 1 {
		t.Fatal(e)
	}
	got, _ := protocol.DecodeJSON(body)
	system := got.(map[string]any)["system"].(string)
	if strings.Contains(system, "tools.opencode") || !strings.Contains(system, "tools.antigravity") ||
		!strings.Contains(system, `tools['antigravity']["worktree_list"]()`) || !strings.Contains(system, "/tmp/opencode opencode.json https://opencode.ai plugins/opencode.so") {
		t.Fatal(system)
	}
	again, s, e := aliasAgentRequest(body)
	if e != nil || again != nil || s != nil {
		t.Fatal("not idempotent", e)
	}
	for _, root := range []map[string]any{{"system": "You are OpenCode.", "tools": []any{codeModeTool()}}, {"system": codeModeSystem, "tools": []any{map[string]any{"name": "execute", "parameters": map[string]any{"type": "object"}}}}} {
		if (openCodeAdapter{}).Match(protocol.InspectRequest(root)) {
			t.Fatal("weak adapter detection")
		}
	}
}
func TestOpenCodeCatalogProvenance(t *testing.T) {
	for _, code := range []string{`return await search({namespace:"opencode"});`, `const r=await search({namespace:"opencode"}); return r;`, `const r=await search({namespace:"opencode"}); return {catalog:r,file:"tools.opencode.read"};`, `return [await search({namespace:"opencode"})];`} {
		a, e := parseAgentJavaScript(code)
		if e != nil {
			t.Fatal(e)
		}
		projection := a.catalogProjection()
		if projection == nil {
			t.Fatal(code)
		}
		catalog := map[string]any{"items": []any{map[string]any{"path": "tools.opencode.worktree_list", "description": "OpenCode source tools.opencode.worktree_list"}}, "remaining": json.Number("0"), "next": nil}
		var payload any = catalog
		if strings.Contains(code, "catalog:r") {
			payload = map[string]any{"catalog": catalog, "file": "tools.opencode.read"}
		}
		if strings.Contains(code, "return [") {
			payload = []any{catalog}
		}
		count := 0
		_, did, e := mapCatalogEnvelope(payload, projection, func(item map[string]any) (bool, error) {
			count++
			item["path"] = "tools.antigravity.worktree_list"
			return true, nil
		})
		if e != nil || !did || count != 1 {
			t.Fatal(e, did, count)
		}
		if catalog["items"].([]any)[0].(map[string]any)["description"] != "OpenCode source tools.opencode.worktree_list" {
			t.Fatal("description changed")
		}
	}
	for _, code := range []string{`return {items:[{path:"tools.opencode.read"}],remaining:0,next:null};`, `const r=await search({}); r=await tools.opencode.read({}); return r;`, `function search(){return {}} return search({});`, `const r=await search({}); return {file:"tools.opencode.read"};`, `const r=await search({}); r.items=await tools.opencode.read({}); return r;`, `const r=await search({}); const alias=r; alias.items=[]; return r;`, `const r=await search({}); mutate(r); return r;`} {
		a, e := parseAgentJavaScript(code)
		if e != nil {
			t.Fatal(e)
		}
		if a.catalogProjection() != nil {
			t.Fatal("unproven catalog accepted", code)
		}
	}
}
func TestOpenCodeRequestResultAndResponse(t *testing.T) {
	for _, withID := range []bool{false, true} {
		root := codeModeRoot()
		call := map[string]any{"name": "execute", "args": map[string]any{"code": `const r=await search({}); return {catalog:r};`}}
		items := []any{
			map[string]any{"path": "tools.opencode.new_codex_tool", "description": "OpenCode"},
			map[string]any{"path": "tools.opencode_root", "description": "OpenCode"},
		}
		result := map[string]any{"name": "execute", "response": map[string]any{"output": map[string]any{"catalog": map[string]any{"items": items, "remaining": 0, "next": nil}}}}
		if withID {
			call["id"] = "search-id"
			result["id"] = "search-id"
		}
		root["contents"] = []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": call}}}, map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": result}, map[string]any{"text": "OpenCode user tools.opencode.x"}}}}
		raw, _ := json.Marshal(root)
		body, s, e := aliasAgentRequest(raw)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(body), "tools.antigravity.new_antigravity_tool") ||
			!strings.Contains(string(body), "tools.antigravity_root") ||
			!strings.Contains(string(body), "OpenCode user tools.opencode.x") {
			t.Fatal(string(body))
		}
		f := map[string]any{"name": "execute", "args": map[string]any{
			"code": `return [tools.antigravity.new_antigravity_tool({content:"tools.antigravity.x"}), tools.antigravity_root()];`,
		}, "id": "keep"}
		if e = s.restoreFunction(f); e != nil {
			t.Fatal(e)
		}
		if f["args"].(map[string]any)["code"] != `return [tools.opencode.new_codex_tool({content:"tools.antigravity.x"}), tools.opencode_root()];` || f["id"] != "keep" {
			t.Fatal(f)
		}
	}
	root := codeModeRoot()
	root["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"items": []any{map[string]any{"path": "tools.opencode.x"}}, "remaining": 0, "next": nil}}}}}}
	before, _ := json.Marshal(root["contents"])
	raw, _ := json.Marshal(root)
	body, _, e := aliasAgentRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := protocol.DecodeJSON(body)
	after, _ := json.Marshal(got.(map[string]any)["contents"])
	if !reflect.DeepEqual(before, after) {
		t.Fatal("unrelated tool result changed")
	}
}

func TestAgentNativeNamespaceRoundTrip(t *testing.T) {
	raw := []byte(`{"system":"You are Codex, a coding agent. Use tools.codex.codex_probe, codex.codex_probe, and tools.codex.unknown.","tools":[{"type":"namespace","name":"codex","tools":[{"type":"function","name":"codex_probe"}]}],"tool_choice":{"type":"allowed_tools","tools":[{"type":"namespace","name":"codex"},{"type":"function","namespace":"codex","name":"codex_probe"}]},"input":[{"type":"function_call","namespace":"codex","name":"codex_probe","arguments":"{\"namespace\":\"codex\"}"}]}`)
	body, s, e := aliasAgentRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := protocol.DecodeJSON(body)
	if root.(map[string]any)["tools"].([]any)[0].(map[string]any)["name"] != "antigravity" {
		t.Fatal(string(body))
	}
	wantPrompt := nativeAntigravityIdentity + " Use tools.antigravity.antigravity_probe, antigravity.antigravity_probe, and tools.codex.unknown."
	if root.(map[string]any)["system"] != wantPrompt {
		t.Fatal(string(body))
	}
	f := map[string]any{"name": "antigravity_probe", "namespace": "antigravity", "arguments": `{"namespace":"codex"}`}
	if e = s.restoreFunction(f); e != nil {
		t.Fatal(e)
	}
	if f["name"] != "codex_probe" || f["namespace"] != "codex" || f["arguments"] != `{"namespace":"codex"}` {
		t.Fatal(f)
	}
	if e = s.restoreFunction(f); e != nil {
		t.Fatal("not idempotent", e)
	}
	if _, _, e = aliasAgentRequest([]byte(`{"system":"You are OpenCode.","tools":[{"type":"namespace","name":"opencode","tools":[]},{"type":"namespace","name":"antigravity","tools":[]}]}`)); e == nil {
		t.Fatal("real namespace collision accepted")
	}
}
func TestOpenCodeAllProtocolCallsAndStreams(t *testing.T) {
	upstream := `return await Promise.all([tools.read({path: file}), tools.antigravity_probe(), tools.antigravity.worktree_list()]);`
	original := `return await Promise.all([tools.read({path: file}), tools.opencode_probe(), tools.opencode.worktree_list()]);`
	system := traceFragment("opencode-v2", "identity") + "\n# Code Mode\n" +
		traceFragment("opencode-v2-code-mode", "header") + "\n" +
		traceFragment("opencode-v2-code-mode", "worktree_list") + "\n  worktrees: Array<string>\n}>\n" +
		traceFragment("opencode-v2-code-mode", "read") + "\n  path: string\n}): Promise<string>\n" +
		"  - tools.opencode_probe(): Promise<string>\n"
	for _, format := range traceProtocols {
		t.Run(format, func(t *testing.T) {
			root := projectTraceRequest(format, []string{system}, []protocol.AgentToolDeclaration{recordedTraceTool("opencode-v2-code-mode")})
			raw, _ := json.Marshal(root)
			body, state, e := aliasAgentRequest(raw)
			if e != nil || state == nil || len(state.adapters) != 1 {
				t.Fatal(e)
			}
			rewritten, _ := protocol.DecodeJSON(body)
			advertised := protocol.PromptTexts(rewritten)[0]
			for _, want := range []string{"tools.read(", "tools.antigravity_probe(", "tools.antigravity.worktree_list("} {
				if !strings.Contains(advertised, want) {
					t.Fatalf("catalog is missing %q: %s", want, advertised)
				}
			}
			var f map[string]any
			var response map[string]any
			args, _ := json.Marshal(map[string]any{"code": upstream, "content": "OpenCode tools.antigravity.worktree_list"})
			switch format {
			case "gemini":
				f = map[string]any{"name": "execute", "args": map[string]any{"code": upstream, "content": "OpenCode tools.antigravity.worktree_list"}}
				response = map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": f, "thoughtSignature": "keep"}}}}}}
			case "chat":
				f = map[string]any{"name": "execute", "arguments": string(args)}
				response = map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "keep", "function": f}}}}}}
			case "responses":
				f = map[string]any{"type": "function_call", "name": "execute", "arguments": string(args), "call_id": "keep"}
				response = map[string]any{"output": []any{f}}
			case "anthropic":
				f = map[string]any{"type": "tool_use", "name": "execute", "input": map[string]any{"code": upstream, "content": "OpenCode tools.antigravity.worktree_list"}, "id": "keep"}
				response = map[string]any{"content": []any{f}}
			}
			if e = state.restoreResponse(response); e != nil {
				t.Fatal(e)
			}
			serialized, _ := json.Marshal(response)
			if !strings.Contains(string(serialized), original) || !strings.Contains(string(serialized), "OpenCode tools.antigravity.worktree_list") {
				t.Fatal(string(serialized))
			}
			var events []string
			switch format {
			case "gemini":
				ev, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "execute", "args": map[string]any{"code": upstream}}}}}}}})
				events = []string{string(ev)}
			case "chat":
				for i, fragment := range []string{string(args[:len(args)/2]), string(args[len(args)/2:])} {
					fn := map[string]any{"arguments": fragment}
					if i == 0 {
						fn["name"] = "execute"
					}
					ev, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "keep", "function": fn}}}}}})
					events = append(events, string(ev))
				}
				events = append(events, `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
			case "anthropic":
				ev, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
				events = []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"keep","name":"execute","input":{}}}`, string(ev), `{"type":"content_block_stop","index":0}`, `{"type":"message_stop"}`}
			case "responses":
				ev, _ := json.Marshal(map[string]any{"type": "response.function_call_arguments.delta", "item_id": "item", "delta": string(args)})
				done, _ := json.Marshal(map[string]any{"type": "response.function_call_arguments.done", "item_id": "item", "arguments": string(args)})
				item, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": "item", "type": "function_call", "name": "execute", "arguments": string(args)}})
				events = []string{`{"type":"response.output_item.added","item":{"id":"item","type":"function_call","name":"execute","arguments":""}}`, string(ev), string(done), string(item), `{"type":"response.completed","response":{"output":[]}}`}
			}
			result := runAliasStream(t, state, events, 2)
			wire, _ := json.Marshal(result)
			if !strings.Contains(string(wire), original) {
				t.Fatal(string(wire))
			}
			if len(state.calls) != 0 {
				t.Fatal("call state leaked")
			}
		})
	}
}

func TestOpenCodeRootOnlyCatalogAndConflicts(t *testing.T) {
	base := "You are OpenCode.\n# Code Mode\n## Code Mode tools (call only inside `execute`)\n"
	tool := recordedTraceTool("opencode-v2-code-mode")
	root := projectTraceRequest("chat", []string{base + "- tools.opencode_open(): Promise<string>\n"}, []protocol.AgentToolDeclaration{tool})
	raw, _ := json.Marshal(root)
	out, state, err := aliasAgentRequest(raw)
	if err != nil || state == nil || len(state.adapters) != 1 || !strings.Contains(string(out), "tools.antigravity_open") {
		t.Fatalf("single member catalog did not round trip: %s %v", out, err)
	}
	call := protocol.AgentToolCall{Object: map[string]any{"name": "execute", "args": map[string]any{
		"code": `return tools["antigravity_open"]();`,
	}}, ArgumentKey: "args"}
	if err := state.adapters[0].RestoreCall(call); err != nil || call.Object["args"].(map[string]any)["code"] != `return tools["opencode_open"]();` {
		t.Fatalf("root member not restored: %v %v", call, err)
	}
	for _, entries := range []string{
		"- tools.opencode(): Promise<string>\n- tools.opencode.read(): Promise<string>\n",
		"- tools.opencode.read(): Promise<string>\n- tools.antigravity(): Promise<string>\n",
		"- tools.opencode_open(): Promise<string>\n- tools.antigravity_open(): Promise<string>\n",
	} {
		body, _ := json.Marshal(projectTraceRequest("responses", []string{base + entries}, []protocol.AgentToolDeclaration{tool}))
		if _, _, err := aliasAgentRequest(body); err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("conflicting paths accepted: %s %v", entries, err)
		}
	}
	fenced := "You are OpenCode.\n```text\n" + base + "- tools.opencode.read(): Promise<string>\n```"
	body, _ := json.Marshal(projectTraceRequest("chat", []string{fenced}, []protocol.AgentToolDeclaration{tool}))
	_, state, err = aliasAgentRequest(body)
	if err != nil || state != nil {
		t.Fatalf("catalog in example activated Code Mode: %v %v", state, err)
	}
}

// liveCodeModeRequest mirrors the structure of the OpenCode requests that
// leaked tools.opencode.* behind "## Available tools": the catalog heading is
// the only part that varies, and the execute description no longer matters.
func liveCodeModeRequest(heading string, tools ...any) []byte {
	system := traceFragment("opencode-v2", "identity") + "\n# Code Mode\n\nThe tools listed below only work inside `execute`.\n\n" + heading +
		"- opencode (2 tools) // Tools for managing OpenCode itself.\n" +
		"  - tools.opencode.worktree_list(): Promise<{\n  worktrees: Array<string>,\n}> // List worktrees.\n" +
		"  - tools.opencode.session_move({\n  directory: string,\n}): Promise<{}> // Move a session.\n\n" +
		"# Rules\n\nCall tools.opencode.session_move only when asked.\n"
	if len(tools) == 0 {
		execute := codeModeTool()
		execute["description"] = "Run JavaScript to compose tool calls."
		tools = []any{execute}
	}
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": []any{map[string]any{"functionDeclarations": tools}}})
	return raw
}

func TestOpenCodeCodeModeIgnoresCatalogHeading(t *testing.T) {
	var outputs []string
	headings := []string{"## Code Mode tools (call only inside `execute`)\n\n", "## Available tools\n\n", "### Anything else\n\n", ""}
	for _, heading := range headings {
		body, state, err := aliasAgentRequest(liveCodeModeRequest(heading))
		if err != nil || state == nil || len(state.adapters) != 1 {
			t.Fatalf("heading %q: adapter not applied: %v", heading, err)
		}
		got, _ := protocol.DecodeJSON(body)
		system := got.(map[string]any)["system"].(string)
		if strings.Contains(system, "opencode") || !strings.Contains(system, "- antigravity (2 tools)") ||
			!strings.Contains(system, "tools.antigravity.worktree_list()") || !strings.Contains(system, "Call tools.antigravity.session_move only") {
			t.Fatalf("heading %q: inconsistent rewrite:\n%s", heading, system)
		}
		outputs = append(outputs, strings.Replace(system, heading, "", 1))

		call := protocol.AgentToolCall{Object: map[string]any{"name": "execute", "args": map[string]any{
			"code": `return await tools.antigravity.session_move({directory:"/tmp"});`,
		}}, ArgumentKey: "args"}
		if err := state.adapters[0].RestoreCall(call); err != nil || call.Object["args"].(map[string]any)["code"] != `return await tools.opencode.session_move({directory:"/tmp"});` {
			t.Fatalf("heading %q: call not restored: %v %v", heading, call.Object, err)
		}
	}
	for i := range outputs {
		if outputs[i] != outputs[0] {
			t.Fatalf("heading changed the rewrite:\n%s\n---\n%s", outputs[0], outputs[i])
		}
	}
}

func TestOpenCodeGroupLabelNeverHalfRewritten(t *testing.T) {
	// No `execute(code)` tool: no plan, so the label must keep the name its
	// paths still use instead of following the prose rules.
	body, _, err := aliasAgentRequest(liveCodeModeRequest("## Available tools\n\n", map[string]any{"name": "read"}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := protocol.DecodeJSON(body)
	system := got.(map[string]any)["system"].(string)
	if !strings.Contains(system, "- opencode (2 tools)") || !strings.Contains(system, "tools.opencode.worktree_list") ||
		!strings.Contains(system, "Tools for managing Antigravity itself") {
		t.Fatalf("label and paths diverged:\n%s", system)
	}
}

func TestOpenCodeAdapterRequiresOpenCodeAgent(t *testing.T) {
	system := "You are Codex, a coding agent.\n- tools.opencode.worktree_list(): Promise<{}>\n"
	raw, _ := json.Marshal(map[string]any{"system": system, "tools": []any{map[string]any{"functionDeclarations": []any{codeModeTool()}}}})
	_, state, err := aliasAgentRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if state != nil && len(state.adapters) != 0 {
		t.Fatal("OpenCode adapter ran for another Agent")
	}
}
