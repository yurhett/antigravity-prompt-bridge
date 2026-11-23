package rewrite

import (
	"github.com/yurhett/antigravity-prompt-bridge/internal/protocol"
)

// Verbatim identity block observed in official agy gen_metadata. CLI-specific
// tools, filesystem paths and permission rules are deliberately not imported.
const nativeAntigravitySystem = "<identity>\n" + nativeAntigravityIdentity + "\nYou are working with a USER to solve their task.\nThe USER will send you requests, which you must always prioritize addressing. User requests are enclosed within <USER_REQUEST> tags.\n</identity>"

func BridgeNativeSystemPrompt(body []byte) ([]byte, bool) {
	return protocol.BridgeSystemPrompt(body, nativeAntigravitySystem)
}
