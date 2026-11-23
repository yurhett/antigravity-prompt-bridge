package plugin

import (
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/yurhett/antigravity-prompt-bridge/internal/rewrite"
)

func TestAgentAliasLimitsAndTerminalCleanup(t *testing.T) {
	p := New(nil)
	store := &p.aliases
	for i := 0; i < maxAliasRequests; i++ {
		if err := store.put(fmt.Sprint(i), &rewrite.AliasState{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.put("overflow", &rewrite.AliasState{}); err == nil {
		t.Fatal("active request limit was not enforced")
	}
	store.remove("0")
	if err := store.put("overflow", &rewrite.AliasState{}); err != nil {
		t.Fatal("released request slot was not reusable", err)
	}
	p.Shutdown()
	if store.get("overflow") != nil {
		t.Fatal("shutdown retained state")
	}
	for _, outcome := range []pluginapi.RequestCompletionOutcome{pluginapi.RequestCompletionSucceeded, pluginapi.RequestCompletionFailed, pluginapi.RequestCompletionCanceled, pluginapi.RequestCompletionRejected} {
		id := "limit-cleanup-" + string(outcome)
		_, s, err := rewriteAliasesForTest([]byte(aliasFixture))
		if err != nil || s == nil {
			t.Fatal(err)
		}
		if err := p.aliases.put(id, s); err != nil {
			t.Fatal(err)
		}
		callResult(t, p, pluginabi.MethodRequestComplete, pluginapi.RequestCompletion{RequestID: id, Outcome: outcome}, nil)
		if p.aliases.get(id) != nil {
			t.Fatal("terminal request retained state", outcome)
		}
	}
}
