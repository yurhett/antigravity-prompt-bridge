# antigravity-prompt-bridge v0.5.4

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
      agent_alias: true
      heuristic_identity: true
      use_default_keywords: true
      custom_mappings: {} # heuristic-only
      audit: false
```

The recommended configuration leaves custom mappings empty. Disable the legacy antigravity-coding-filter to avoid processing requests twice. Supported fragment templates and their sources are recorded in `internal/rewrite/assets/agent-prompt-templates.json`. Templates and heuristic identity normalization can use this verified native sentence:

> You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.

`heuristic_identity` controls whole-sentence identity normalization for unidentified requests; an identified agent's self-introduction is always replaced with the native Antigravity identity sentence. In the legacy path, turning it off enables keyword rewriting according to `use_default_keywords`. Identified agents continue to use templates and whole-word cleanup while `agent_alias` is enabled. Whole-word cleanup skips lowercase ambiguous names used as ordinary nouns (`move the cursor`, `a goose`, `your AI copilot`, `cascade of errors`). The brand catalogue records each family's `vendors`; only attribution phrases such as `built/created/developed by X`, `a product of X`, `X's official CLI`, `(formerly X)` and `由 X 开发` are credited to the Google Deepmind team or removed, while functional references such as `Anthropic API` or `Microsoft content policies` are kept. Only `rewrite` mode is supported.

Scope covers system/developer messages, Responses `instructions`, and Gemini `systemInstruction`. Heuristics protect quoted examples, Markdown quotes/links, code, paths/URLs, and recognized XML project/example/untrusted blocks. Separate behavioral clauses are retained; ambiguous embedded restrictions are skipped. Recognition is primarily English with limited Chinese support and cannot match every possible identity formulation. Explicit custom mappings remain authoritative within the existing mapping protection rules.

## Native Antigravity prompt bridge

`antigravity_bridge_system_prompt` defaults to **true** and applies only to the selected native `antigravity` provider. Identity rewriting runs first. The processed system/developer text then moves into a new user message wrapped in `<USER_REQUEST>`, with `--- message N role=system/developer ---` text labels. The actual system field becomes the verified original Antigravity `<identity>` block. This follows the observed agy placement while retaining native API tool schemas and conversation history. It does not import agy's CLI-specific filesystem, tools or permission instructions.

OpenAI Chat Completions, Responses, Anthropic Messages and Gemini requests (including the Antigravity `request` envelope and snake-case system field) are supported. Existing user/assistant/tool messages, tool definitions, tool results, generation settings and metadata remain unchanged; the bridge inserts one user message before history and replaces/removes only system/developer prompt fields. Mixed non-text system content is not relocated, to avoid losing unsupported blocks. Repeated processing does not duplicate the wrapper.

Set this switch to `false` to retain native system-field placement while keeping the existing rewriting behavior. `enable_antigravity: false` bypasses both rewriting and bridging for that provider. `agy` retains its existing rewrite path regardless of the bridge switch. The role labels in the wrapper are ordinary text: relocated instructions no longer occupy the API's system/developer role. Turning on this bridge does not guarantee upstream availability or tool success.

Whole-sentence identity normalization skips fields larger than 256 KiB and candidate sentences larger than 2048 bytes, and limits identity rewrites to 128 per field. Optional audit logs contain request IDs, body hashes, recognized agent families, template hits, fixed residuals, and protected residuals. Prompt text is excluded.

## Agent traces

Download `agent-traces-all` from the Actions run summary for captures, replay, and concrete changes. Starting with `v0.5.4`, a successful release capture is attached to its GitHub Release as `agent-traces-baseline.zip`. Scheduled and manual runs compare against the latest stable tag. Release runs compare against the previous stable tag.

## Build and verify

### Agent release and request tracking

The **Agent traces** GitHub Actions workflow runs daily at 09:23 Malaysia time. A manual run can select one release channel. It tracks stable OpenCode V1 and V2, Pi, Codex, Gemini CLI, and Claude Code. Each installed CLI sends its own requests while working in a synthetic Git project against an isolated loopback endpoint. OpenCode also probes the Code Mode catalog and `execute` round trip. [Workflow](.github/workflows/agent-traces.yml)

Lane artifacts retain the exact release and source, sanitized request bodies, native ABI replay, and review candidates for 30 days. Comparisons use the last successful sample from the same release channel and capture configuration. Review candidates enter the runtime rules through code review. The loopback replies are scripted, so this measures client requests and tool behavior. Model interpretation needs a separate evaluation.

### Plugin build

Build tools are pinned to Go 1.27.3 and Zig 0.17.1. Run `go test -race -count=1 ./...` and `go vet ./...`, then `sh scripts/build.sh` to build a C shared library for Linux amd64, GOAMD64=v1, GNU libc 2.34+. ABI checks use Python 3.12+ standard library. Unit tests cover protocol boundaries, verified identities, independent provider switches, bridge placement, tool/history preservation, constraints, quoting and Unicode. Fuzz tests cover idempotence and quoted unknown identities. `scripts/abi_smoke.py` validates the compiled Linux library with a mock host; it makes no upstream request. `scripts/native_bridge_abi.py` accepts the compiled library and optional corpus/output paths to test both bridge states across both providers and four protocols.

## Install and roll back

Repository, runtime plugin ID, store name, configuration key and asset prefix all use `antigravity-prompt-bridge`. Add this CPA custom store source:

```text
https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
```

Download `antigravity-prompt-bridge_0.5.4_linux_amd64.zip` and `checksums.txt` from [Releases](https://github.com/yurhett/antigravity-prompt-bridge/releases/latest), verify the SHA256, and extract the root-level `antigravity-prompt-bridge.so`.

When upgrading from this project's v0.3.0, move the configuration from `plugins.configs.agy-coding-filter` to `plugins.configs.antigravity-prompt-bridge`, preserving all settings. Remove the old configuration key and binary from plugin discovery before enabling the new plugin. The v0.3.0 tag and Release remain available for rollback. Prompt processing and defaults are unchanged by the rename.

Back up the old binary outside CPA's plugin discovery directory. Install as `antigravity-prompt-bridge-v0.5.4.so` in the configured plugins directory and save plugin configuration in CPAMC to trigger hot reload. A changed versioned file path is required for replacement. Confirm runtime version and boolean settings above. To bypass only one provider, turn off its switch. To roll back placement only, disable `antigravity_bridge_system_prompt`. To roll back the binary, remove the new versioned file, restore the backed-up binary, and reload configuration.

This is an independently maintained adaptation. Source attribution is preserved for jellyfish-p/cpa-plugin-antigravity-coding-filter v0.3.1 (MIT). The stock CLIProxy Antigravity plugin remains independent.

`agent_alias` defaults to true. Each registered agent can supply identity evidence, prompt fragments, and an execution adapter. It uses the original system text and declared tools. Recognized agents share the same template and whole-word cleanup pipeline across all four protocols. Claude Code drops reviewed vendor help, product announcements, and model-selection guidance. Gemini CLI checks its identity, built-in help declaration, and `invoke_agent` schema while retaining its non-interactive role and help behavior. Pi's `pi agent` can be rewritten while `api`, `pipeline`, and `PiAgent` are preserved. A declared `pi_read` and its references share a reversible alias. Unidentified requests and requests with `agent_alias` disabled use the legacy rewrite settings.

Tool declarations and parameter schemas use the same text pipeline for `description` strings. Parameter names, enums, defaults, call arguments, results, and message history retain their original values. Agent-specific adapters handle execution contracts such as OpenCode Code Mode.

The OpenCode adapter verifies the formal system catalog and execute/code schema. It handles one- and two-member paths such as `tools.read` and `tools.opencode.worktree_list`, along with static search namespace selectors and catalog results proven by call ID and the AST. The client receives the original executable identifiers. The pinned Goja parser edits source spans and preserves ordinary strings, comments, arguments, result bodies, and user messages. All four protocols share the adapter plan and streaming restoration.
