# antigravity-prompt-bridge

[![Release](https://img.shields.io/github/v/release/yurhett/antigravity-prompt-bridge)](https://github.com/yurhett/antigravity-prompt-bridge/releases/latest)

[中文](README.md) · [Example config](examples/config.yaml) · [Changelog](CHANGELOG.md) · [Attribution & license](NOTICE.md)

A CLIProxyAPI (CPA) native ABI v1 plugin that processes prompts for `agy` and native `antigravity`.

- Latest public release is **v0.5.4** (Linux amd64 shared library). The `brand_guard` and `unidentified` settings below apply to v0.5.5.
- Repository, plugin ID, store name, configuration key and asset prefix all use **`antigravity-prompt-bridge`**

## Contents

- [Features](#features)
- [Quick start](#quick-start)
- [Installation](#installation)
- [Configuration](#configuration)
- [How it works](#how-it-works)
- [Upgrade and rollback](#upgrade-and-rollback)
- [Known limits](#known-limits)
- [License](#license)

## Features

| Feature | Description |
|---|---|
| Agent prompt rewriting | Identifies the agent from its self-description, applies fragment templates, then cleans up residual brand names by whole word; optional whole-sentence identity normalization |
| Native prompt bridge | Native `antigravity` only: rewrites first, then moves caller system/developer prompts into a `<USER_REQUEST>` wrapper in a user message |
| Independent switches | `agy` and `antigravity` can be disabled separately; the bridge switch is on by default and does not change how `agy` is processed |
| Protocol support | Chat Completions, Responses, Anthropic Messages, Gemini and the Antigravity `request` envelope; tool names are rewritten reversibly |

## Quick start

1. Add the plugin source to your CPA main configuration:

   ```yaml
   plugins:
     enabled: true
     store-sources:
       - https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
   ```

2. Refresh the store and install **antigravity-prompt-bridge** (ID: `antigravity-prompt-bridge`).
3. Merge the [recommended configuration](#recommended-configuration) and save to trigger hot reload.
4. Disable older filters with the same function (such as `agy-coding-filter` or `antigravity-coding-filter`) so requests are not rewritten twice.

## Installation

### Requirements

- Linux x86-64, GNU libc **2.34+** (Alpine/musl not supported)
- A CPA build that supports native ABI v1, request interceptors and `host.auth.get_runtime`
- Built against CPA SDK `v7.3.2`

### Option 1: CPA custom plugin source (recommended)

See [Quick start](#quick-start). Use this project's source so the repository URL and plugin ID match.

### Option 2: Manual installation

1. Download `antigravity-prompt-bridge_0.5.4_linux_amd64.zip` and `checksums.txt` from [Releases](https://github.com/yurhett/antigravity-prompt-bridge/releases).
2. Verify and extract:

   ```sh
   sha256sum -c checksums.txt
   unzip antigravity-prompt-bridge_0.5.4_linux_amd64.zip
   ```

3. Place `antigravity-prompt-bridge.so` in the CPA plugin discovery directory, preferably named `antigravity-prompt-bridge-v0.5.4.so`. Keep backups of older versions outside the discovery directory.
4. Save the plugin configuration to trigger hot reload, and confirm the loaded version is `0.5.4`.

## Configuration

### Recommended configuration

Merge into your existing `plugins.configs`, keeping other plugins' settings:

```yaml
plugins:
  enabled: true
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
      brand_guard: reject
      unidentified: reject
      use_default_keywords: true
      custom_mappings: {}
      audit: false
```

### Options

| Field | Default | Description |
|---|---|---|
| `mode` | `rewrite` | Only `rewrite` is supported |
| `enable_agy` | `true` | Process requests whose selected credential is agy |
| `enable_antigravity` | `true` | Process requests whose selected credential is native Antigravity |
| `antigravity_bridge_system_prompt` | `true` | Native Antigravity: rewrite, then relocate; does not affect agy. Turn off to keep the native system-field placement |
| `agent_alias` | `true` | For identified agents, enables the standard identity sentence, templates, whole-word brand cleanup and tool aliases. Unidentified requests follow `unidentified`; when off, every request takes the legacy path |
| `heuristic_identity` | `false` (recommended on) | Heuristic whole-sentence identity normalization for unidentified requests; identified agents are always normalized. When off, the legacy path uses keyword replacement |
| `brand_guard` | `reject` | What happens when an identified agent misses an expected rule: `off` / `report` / `reject` / `strict`, see below |
| `unidentified` | `reject` | Policy when no agent is identified: `reject` / `legacy`, see below |
| `use_default_keywords` | `true` | Default legacy keyword presets and heuristic identity cues |
| `custom_mappings` | `{}` | Explicit system-text mappings; take precedence over other rules |
| `audit` | `false` | Logs request IDs, body hashes, agent, template hits, fixed residuals and protected residuals; prompt text is never logged |

## How it works

### Scope

Whether a request is processed depends on the actual provider looked up from `selected_auth_index`, not on the model name. Requests the plugin executes itself without CPA credential scheduling have no such index and pass through unchanged; bare model names may be picked up by other plugins along existing routes.

Rewriting covers system/developer messages, Responses `instructions` and Gemini `systemInstruction`. Heuristics protect the following and leave them unchanged:

- Quoted examples, Markdown quotes and links
- Code, paths and URLs
- Recognized XML project, example and untrusted-content blocks

Separate behavioral clauses are retained; ambiguous embedded restrictions are skipped. Explicit `custom_mappings` remain authoritative within the existing mapping protection rules. Supported fragment templates and their sources are recorded in `internal/rewrite/assets/agent-prompt-templates.json`.

### Bridge layout

Identity sentence:

> You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.

```text
System field: original Antigravity <identity> block

New user message:
  <USER_REQUEST>
  Follow the system and developer instructions ...
  --- message 1 role=system ---
  Caller system prompt with the identity sentence rewritten; the rest is kept
  </USER_REQUEST>

Existing user messages, assistant messages and tool results: kept in original order
```

- The `role=system/developer` labels inside the wrapper are plain text; the content actually belongs to a user message.
- Official CLI tool, directory and permission instructions are not imported.
- Unsupported non-text system blocks stay in place; repeated processing does not wrap twice.

### Agent recognition and rewriting

`agent_alias` identifies the agent from the original system text and the declared tools. Processing order:

1. Explicit `custom_mappings`
2. Declared tool aliases
3. Agent fragment templates
4. Whole-word brand cleanup

| Agent | Rules |
|---|---|
| Claude Code | Drops reviewed vendor help, model recommendations and product announcements |
| Gemini CLI | Checks the identity sentence plus the `cli_help` and `invoke_agent` declarations; keeps non-interactive mode and help-tool behavior |
| Pi | Rewrites `pi agent`, keeps `api`, `pipeline` and `PiAgent`; `pi_read` and its references share one reversible alias |
| OpenCode | Supports Code Mode, see below |

Brand cleanup rules:

- Lowercase ambiguous words used as ordinary nouns are skipped, e.g. `move the cursor`, `a goose`, `your AI copilot`, `cascade of errors`.
- Only attribution phrases (`built/created/developed by X`, `a product of X`, `X's official CLI`, `(formerly X)`, `由 X 开发`) are credited to the Google Deepmind team or removed.
- Functional references such as `Anthropic API` or `Microsoft content policies` are kept.

### Rule coverage (`brand_guard`)

An adapter can fall behind a client update and miss what it should rewrite. `brand_guard` counts rule hits instead of scanning the prompt, so project notes, paths and commands that the rules keep on purpose never count as gaps. An identified agent must hit:

| Rule | Level | Meaning |
|---|---|---|
| `identity:<Agent>` | gap | The self-introduction became the native Antigravity identity, by a template or by identity normalization |
| `identity-template:<Agent>` | drift | When the agent has recorded identity templates, at least one matched. A miss means the client reworded its identity, which normalization already replaced |
| `adapter:<adapter>` | gap | When the request declares the adapter's feature, the adapter applied. For example, a declared `execute(code: string)` requires OpenCode Code Mode |
| `adapter:<adapter>/<rule>` | gap | Rules an adapter declares itself, such as `adapter:opencode-code-mode/group-labels`: every catalog group label is renamed together with its paths |

A gap sends agent features upstream; drift is already handled and only means a template is stale.

| Value | Gap | Drift | Use |
|---|---|---|---|
| `off` | Nothing | Nothing | — |
| `report` | Log at warn level, forward | Log | Observing |
| `reject` (default) | Return 400 `agent_bridge_coverage_gap` without calling upstream | Log | Everyday use |
| `strict` | Return 400 | Return 400 | Developing adapters |

A 400 response lists each rule's level, hits and expected count in `error.coverage`. Logs never contain prompt text.

### Unidentified requests (`unidentified`)

When `agent_alias` is on and no agent is identified:

| Value | Behavior |
|---|---|
| `reject` (default) | Checks agent fingerprints: a request carrying every fingerprint feature of a known agent returns 400 `agent_bridge_unidentified` without calling upstream; any other request gets only the native Antigravity identity sentence and is otherwise forwarded unchanged |
| `legacy` | Legacy keyword or heuristic rewrite (see `heuristic_identity`), no identity sentence |

**Fingerprints**: each known agent has two highly distinctive features taken from recorded requests, listed in `internal/rewrite/assets/agent-fingerprints.json`. A feature is a piece of system/developer text or a tool declared with a given parameter; user messages are never checked. Both features mean the request comes from that agent but its identity changed and was not recognized; forwarding it would carry its features, so it is refused with a hint to update the plugin. A single feature is forwarded and logged at warn level. Covered agents: OpenCode, Codex, Claude Code, Gemini CLI and Pi.

**Identity sentence**: placed in front of the first system/developer instruction, unless one already starts with it. Without instructions, a field is created for the Gemini, OpenAI Chat, Responses or Anthropic format; nothing is added when the format is unknown.

With `agent_alias` off, every request takes the legacy path: no fingerprint check and no identity sentence.

### Tool aliases

- `description` strings in tool declarations and parameter schemas go through the same templates and cleanup.
- Parameter names, enums, defaults, call arguments, ordinary tool results, file contents and message history are unchanged.
- Aliases are reversible: original names are restored before the client executes a tool.
- **OpenCode Code Mode**: supports static paths such as `tools.read` and `tools.opencode.worktree_list`; catalog references, `search` and `execute.code` share one mapping. Dynamic paths or calls that cannot be safely restored return an explicit error.

## Upgrade and rollback

### Migrating from the old name (v0.3.0 → v0.3.1+)

1. Move the configuration under `plugins.configs.agy-coding-filter` unchanged to `plugins.configs.antigravity-prompt-bridge`.
2. Remove the old configuration key and the old shared library from the discovery directory.
3. Refresh the custom source, install the new plugin, and confirm only one version is active.
4. Disable the old `antigravity-coding-filter`, which has the same function.

The rename does not change the processing algorithm or defaults. The old v0.3.0 tag and Release remain available for rollback.

### Rollback

- **Bridge placement only**: turn off `antigravity_bridge_system_prompt`.
- **Bypass one provider**: turn off `enable_agy` or `enable_antigravity`.
- **Binary**: remove the new versioned file, restore the backup, and save the configuration to trigger hot reload. Do not keep multiple binaries with the same ID in the discovery directory.

## Known limits

- Heuristics are primarily English with limited Chinese support and cannot cover every future template.
- Prompt fields larger than 256 KiB skip heuristic identity rewriting; candidate sentences are capped at 2,048 bytes, with at most 128 rewrites per field.
- The plugin does not guarantee upstream quota or availability.

## License

An independently maintained MIT-licensed adaptation. See [NOTICE](NOTICE.md) for original authors, reference projects and test materials, and [LICENSE](LICENSE) for the full license.
