package protocol

// Only native protocol name slots are eligible. Never descend into arguments,
// result payloads, schemas, text, signatures, encrypted content, or media.
func VisitResponseCalls(value any, visit func(map[string]any) error) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	restoreBlock := func(v any) error {
		b, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if b["type"] == "function_call" || b["type"] == "custom_tool_call" || b["type"] == "tool_use" {
			return visit(b)
		}
		return nil
	}
	if err := restoreBlock(object); err != nil {
		return err
	}
	if response, ok := object["response"].(map[string]any); ok {
		if err := VisitResponseCalls(response, visit); err != nil {
			return err
		}
	}
	for _, key := range []string{"item", "content_block"} {
		if err := restoreBlock(object[key]); err != nil {
			return err
		}
	}
	for _, key := range []string{"output", "content"} {
		if blocks, ok := object[key].([]any); ok {
			for _, b := range blocks {
				if err := restoreBlock(b); err != nil {
					return err
				}
			}
		}
	}
	if candidates, ok := object["candidates"].([]any); ok {
		for _, v := range candidates {
			c, _ := v.(map[string]any)
			content, _ := c["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, v := range parts {
				p, _ := v.(map[string]any)
				for _, field := range []string{"functionCall", "function_call"} {
					if f, ok := p[field].(map[string]any); ok {
						if err := visit(f); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if choices, ok := object["choices"].([]any); ok {
		for _, v := range choices {
			c, _ := v.(map[string]any)
			for _, key := range []string{"message", "delta"} {
				m, _ := c[key].(map[string]any)
				if f, ok := m["function_call"].(map[string]any); ok {
					if err := visit(f); err != nil {
						return err
					}
				}
				calls, _ := m["tool_calls"].([]any)
				for _, v := range calls {
					t, _ := v.(map[string]any)
					if f, ok := t["function"].(map[string]any); ok {
						if err := visit(f); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}
