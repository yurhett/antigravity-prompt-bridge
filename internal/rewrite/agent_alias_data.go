package rewrite

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yurhett/antigravity-prompt-bridge/internal/config"
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
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
	return token == config.PluginName
}

func isPluginPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if i+1 < len(parts) && parts[i+1] != "" && (part == "plugin" || part == "plugins" || part == config.PluginName) {
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

func declaredToolReferences(declarations []protocol.ToolDeclaration, alias func(string) string) map[string]string {
	references := make(map[string]string)
	for _, declaration := range declarations {
		name := declaration.Object["name"].(string)
		upstream := alias(name)
		ns, upstreamNS := declaration.Namespace, alias(declaration.Namespace)
		if declaration.NamespaceOnly {
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
