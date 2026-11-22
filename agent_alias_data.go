package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

func protectedBrandReference(token string) bool {
	if strings.ContainsAny(token, `/\\`) || strings.Contains(token, "://") || strings.HasPrefix(token, "@") {
		return true
	}
	if strings.HasPrefix(token, "tools.") || strings.HasPrefix(token, "tools[") {
		return false
	}
	for _, suffix := range []string{".com", ".org", ".net", ".ai", ".io", ".app", ".co", ".uk", ".json", ".jsonc", ".yaml", ".yml", ".toml", ".ts", ".js", ".py", ".go", ".so", ".md", ".txt", ".exe", ".zip"} {
		if strings.HasSuffix(strings.ToLower(token), suffix) {
			return true
		}
	}
	return token == pluginName
}

func isPluginPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if i+1 < len(parts) && parts[i+1] != "" && (part == "plugin" || part == "plugins" || part == pluginName) {
			return true
		}
	}
	return false
}

func brandPathToken(text string, start, end int) (string, bool) {
	pathByte := func(c byte) bool {
		return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune(`_./:@~\-`, rune(c))
	}
	for start > 0 && pathByte(text[start-1]) {
		start--
	}
	for end < len(text) && pathByte(text[end]) {
		end++
	}
	token := strings.TrimRight(text[start:end], ".:")
	return token, strings.ContainsAny(token, "/\\.:@_-")
}

func declaredToolReferences(declarations []toolDeclaration, alias func(string) string) map[string]string {
	references := make(map[string]string)
	for _, declaration := range declarations {
		name := declaration.object["name"].(string)
		upstream := alias(name)
		ns, upstreamNS := declaration.namespace, alias(declaration.namespace)
		if declaration.namespaceOnly {
			if upstream != name {
				references["tools."+name] = "tools." + upstream
			}
			continue
		}
		if upstream != name {
			references[name] = upstream
		}
		if ns != "" && (ns != upstreamNS || name != upstream) {
			references[ns+"."+name] = upstreamNS + "." + upstream
			references["tools."+ns+"."+name] = "tools." + upstreamNS + "." + upstream
		}
	}
	return references
}

var dottedToolMember = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Code Mode catalog declarations and documented calls may use either dot or
// bracket notation. Each spelling carries the same adapter-validated alias.
func codeToolMemberAliases(client, upstream string) [][2]string {
	quotedClient, _ := json.Marshal(client)
	quotedUpstream, _ := json.Marshal(upstream)
	pairs := [][2]string{{"[" + string(quotedClient) + "]", "[" + string(quotedUpstream) + "]"}}
	if !strings.ContainsAny(client+upstream, "'\\\n\r") {
		pairs = append(pairs, [2]string{"['" + client + "']", "['" + upstream + "']"})
	}
	if dottedToolMember.MatchString(client) && dottedToolMember.MatchString(upstream) {
		pairs = append(pairs, [2]string{"." + client, "." + upstream})
	}
	return pairs
}

var toolSchemaKeys = []string{"parameters", "input_schema", "parametersJsonSchema", "parameters_json_schema"}

func rewriteToolDocumentation(declaration map[string]any, rewrite func(string) (string, bool)) bool {
	changed := rewriteDescription(declaration, rewrite)
	for _, key := range toolSchemaKeys {
		changed = rewriteSchemaDescriptions(declaration[key], rewrite) || changed
	}
	return changed
}

func rewriteDescription(object map[string]any, rewrite func(string) (string, bool)) bool {
	if text, ok := object["description"].(string); ok {
		if next, changed := rewrite(text); changed {
			object["description"] = next
			return true
		}
	}
	return false
}

func rewriteSchemaDescriptions(value any, rewrite func(string) (string, bool)) bool {
	schema, ok := value.(map[string]any)
	if !ok {
		return false
	}
	changed := rewriteDescription(schema, rewrite)
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependencies", "dependentSchemas"} {
		children, _ := schema[key].(map[string]any)
		for _, child := range children {
			changed = rewriteSchemaDescriptions(child, rewrite) || changed
		}
	}
	for _, key := range []string{"items", "prefixItems", "anyOf", "any_of", "allOf", "oneOf", "additionalItems", "additionalProperties", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "not", "if", "then", "else"} {
		switch child := schema[key].(type) {
		case []any:
			for _, item := range child {
				changed = rewriteSchemaDescriptions(item, rewrite) || changed
			}
		case map[string]any:
			changed = rewriteSchemaDescriptions(child, rewrite) || changed
		}
	}
	return changed
}
