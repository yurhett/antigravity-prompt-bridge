# antigravity-prompt-bridge v0.3.1

Independent CPA native ABI v1 plugin for Linux amd64. Filters caller system/developer instructions for the selected `agy` or `antigravity` credential. Provider selection uses `selected_auth_index` and read-only `host.auth.get_runtime`; model prefixes and client protocol do not determine scope. Unknown credentials pass through.

Both provider switches default to true and can be disabled independently. `heuristic_identity` defaults to false for compatibility; the recommended configuration enables it explicitly:

```yaml
plugins:
  configs:
    antigravity-prompt-bridge:
      enabled: true
      priority: 1
      mode: rewrite
      enable_agy: true
      enable_antigravity: true
      antigravity_bridge_system_prompt: true
      heuristic_identity: true
      use_default_keywords: true
      custom_mappings: {} # heuristic-only
      audit: false
```

The recommended configuration has no exact custom mappings. Disable the legacy antigravity-coding-filter to avoid processing requests twice. The optional custom-mapping feature remains for compatibility but is unused. Historical source identities in `testdata/verified-identities.json` are test fixtures, not active mappings; synthetic test sentences are also not source mappings. The algorithm then conservatively recognizes first/second-person agent identity and harness declarations using sentence structure, product names, and AI/agent/creator/runtime features. It normalizes recognized identities to the verified native sentence:

> You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.

Built-in keywords become identity clues, rather than broad product-name substitutions. Turning heuristics off restores the original keyword rewrite behavior. Only `rewrite` mode is supported.

Scope covers system/developer messages, Responses `instructions`, and Gemini `systemInstruction`. Heuristics protect quoted examples, Markdown quotes/links, code, paths/URLs, and recognized XML project/example/untrusted blocks. Separate behavioral clauses are retained; ambiguous embedded restrictions are skipped. Recognition is primarily English with limited Chinese support and cannot match every possible identity formulation. Explicit custom mappings remain authoritative within the existing mapping protection rules.

## Native Antigravity prompt bridge

`antigravity_bridge_system_prompt` defaults to **true** and applies only to the selected native `antigravity` provider. Identity rewriting runs first. The processed system/developer text then moves into a new user message wrapped in `<USER_REQUEST>`, with `--- message N role=system/developer ---` text labels. The actual system field becomes the verified original Antigravity `<identity>` block. This follows the observed agy placement while retaining native API tool schemas and conversation history. It does not import agy's CLI-specific filesystem, tools or permission instructions.

OpenAI Chat Completions, Responses, Anthropic Messages and Gemini requests (including the Antigravity `request` envelope and snake-case system field) are supported. Existing user/assistant/tool messages, tool definitions, tool results, generation settings and metadata remain unchanged; the bridge inserts one user message before history and replaces/removes only system/developer prompt fields. Mixed non-text system content is not relocated, to avoid losing unsupported blocks. Repeated processing does not duplicate the wrapper.

Set this switch to `false` to retain native system-field placement while keeping the existing rewriting behavior. `enable_antigravity: false` bypasses both rewriting and bridging for that provider. `agy` retains its existing rewrite path regardless of the bridge switch. The role labels in the wrapper are ordinary text: relocated instructions no longer occupy the API's system/developer role. Turning on this bridge does not guarantee upstream availability or tool success.

No network or model calls are used. Heuristic processing skips fields larger than 256 KiB and candidate sentences larger than 2048 bytes, and limits rewrites to 128 per field. Optional audit logs contain request IDs and body hashes, without prompt text; leave audit disabled normally. This plugin does not change the official agy CLI or guarantee upstream quota/availability.

## Build and verify

Build tools are pinned to Go 1.27.3 and Zig 0.17.1. Run `go test -race -count=1 ./...` and `go vet ./...`, then `sh scripts/build.sh` to build a C shared library for Linux amd64, GOAMD64=v1, GNU libc 2.34+. ABI checks use Python 3.12+ standard library. Unit tests cover protocol boundaries, verified identities, independent provider switches, bridge placement, tool/history preservation, constraints, quoting and Unicode. Fuzz tests cover idempotence and quoted unknown identities. `scripts/abi_smoke.py` validates the compiled Linux library with a mock host; it makes no upstream request. `scripts/native_bridge_abi.py` accepts the compiled library and optional corpus/output paths to test both bridge states across both providers and four protocols.

## Install and roll back

Repository, runtime plugin ID, store name, configuration key and asset prefix all use `antigravity-prompt-bridge`. Add this CPA custom store source:

```text
https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
```

Download `antigravity-prompt-bridge_0.3.1_linux_amd64.zip` and `checksums.txt` from [Releases](https://github.com/yurhett/antigravity-prompt-bridge/releases/latest), verify the SHA256, and extract the root-level `antigravity-prompt-bridge.so`.

When upgrading from this project's v0.3.0, move the configuration from `plugins.configs.agy-coding-filter` to `plugins.configs.antigravity-prompt-bridge`, preserving all settings. Remove the old configuration key and binary from plugin discovery before enabling the new plugin. The v0.3.0 tag and Release remain available for rollback. Prompt processing and defaults are unchanged by the rename.

Back up the old binary outside CPA's plugin discovery directory. Install as `antigravity-prompt-bridge-v0.3.1.so` in the configured plugins directory and save plugin configuration in CPAMC to trigger hot reload. A changed versioned file path is required for replacement. Confirm runtime version and boolean settings above. To bypass only one provider, turn off its switch. To roll back placement only, disable `antigravity_bridge_system_prompt`; to roll back the binary, remove the new versioned file and restore the backed-up binary, then reload configuration.

This is an independently maintained adaptation. Source attribution is preserved for jellyfish-p/cpa-plugin-antigravity-coding-filter v0.3.1 (MIT). The stock CLIProxy Antigravity plugin remains independent.
