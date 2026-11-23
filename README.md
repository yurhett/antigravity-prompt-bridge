# antigravity-prompt-bridge

[![CI and Release](https://github.com/yurhett/antigravity-prompt-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/yurhett/antigravity-prompt-bridge/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yurhett/antigravity-prompt-bridge)](https://github.com/yurhett/antigravity-prompt-bridge/releases/latest)

[English](README.en.md) · [配置示例](examples/config.yaml) · [来源与许可](NOTICE.md)

CLIProxyAPI（CPA）原生 ABI v1 插件，为 `agy` 和原生 `antigravity` 提供提示词处理。当前版本 **v0.5.4**，发布 Linux amd64 动态库。

仓库名、运行时插件 ID、商店名称、配置键和发布包前缀统一为 **`antigravity-prompt-bridge`**。

## 功能

- **Agent 提示词改写**：从自我声明识别 Agent，先应用已有来源的片段模板，再按整词修正残留品牌。可启用整句身份归一化。
- **原生提示词桥接**：只对原生 `antigravity` 生效，先改写，再将调用方 system/developer 提示词放入用户消息的 `<USER_REQUEST>` 包装中。独立系统字段保留 Antigravity 原版 `<identity>` 块。
- **独立开关**：`agy` 和 `antigravity` 可分别停用；搬移开关默认开启，不改变 `agy` 原有的处理方式。
- **协议适配**：支持 Chat Completions、Responses、Anthropic Messages、Gemini 及 Antigravity `request` 外层，可逆地改写工具名称及其文档引用，保留调用契约和历史正文。

身份句使用：

> You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.

桥接后的逻辑布局：

```text
系统字段：Antigravity 原版身份块

新增用户消息：
  <USER_REQUEST>
  Follow the system and developer instructions ...
  --- message 1 role=system ---
  已改写身份句的调用方系统提示词，其余正文保留
  </USER_REQUEST>

原有用户问题、助手消息和工具结果：按原顺序保留
```

包装内的 `role=system/developer` 是文本标签，搬移后的提示词实际属于用户消息。桥接不会移植官方 CLI 的工具、目录或权限说明。含不支持的非文本系统块时保留原位置；重复处理不会重复包装。

## 安装

### 使用 CPA 自定义插件源

在 CPA 的插件商店中添加自定义源，或将以下地址追加到主配置的 `plugins.store-sources`：

```text
https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
```

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
```

刷新商店并安装 **antigravity-prompt-bridge**（ID：`antigravity-prompt-bridge`）。请使用本项目的自定义源，确保仓库地址和插件 ID 对应正确。

### 从旧名称迁移

v0.3.1 起使用统一名称 `antigravity-prompt-bridge`。从本项目 v0.3.0 升级时，将 `plugins.configs.agy-coding-filter` 下的原配置迁移到 `plugins.configs.antigravity-prompt-bridge`，保留字段和值，再移除旧配置键和插件发现目录中的旧动态库。刷新自定义源后安装新插件，确认只有一个版本生效，避免两个插件重复处理。同功能的旧 `antigravity-coding-filter` 也应停用。

旧 v0.3.0 标签与 Release 保留供回退，当前发布使用新名称。更名不改变提示词处理算法和默认开关。

### 手动安装

1. 从 [Releases](https://github.com/yurhett/antigravity-prompt-bridge/releases) 下载 `antigravity-prompt-bridge_0.5.4_linux_amd64.zip` 和 `checksums.txt`。
2. 在下载目录验证并解压：

   ```sh
   sha256sum -c checksums.txt
   unzip antigravity-prompt-bridge_0.5.4_linux_amd64.zip
   ```

3. 将解压出的 `antigravity-prompt-bridge.so` 安装到 CPA 插件发现目录，建议命名为 `antigravity-prompt-bridge-v0.5.4.so`。旧版本备份放到发现目录以外。
4. 保存 CPA 插件配置以触发热加载，并确认已加载版本为 `0.5.4`。

要求 Linux x86-64、GNU libc **2.34 或更新版本**，以及支持原生 ABI v1、请求拦截器和 `host.auth.get_runtime` 的 CPA。Alpine/musl 不适用本发布包。当前依赖 CPA SDK `v7.3.2`。

## 推荐配置

将配置合并到现有 `plugins.configs`，保留其他插件设置：

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
      use_default_keywords: true
      custom_mappings: {}
      audit: false
```

| 字段 | 代码默认值 | 含义 |
|---|---|---|
| `enable_agy` | `true` | 对实际选中的 agy 认证启用过滤 |
| `enable_antigravity` | `true` | 对实际选中的原生 Antigravity 认证启用处理 |
| `antigravity_bridge_system_prompt` | `true` | 原生 Antigravity 先改写、再搬移；不影响 agy |
| `agent_alias` | `true` | 识别成功后将自我介绍句换成 Antigravity 标准身份句，并启用模板、品牌整词补漏与工具别名；未识别或关闭时使用旧模式 |
| `heuristic_identity` | `false` | 控制未识别请求的启发式整句身份归一化，推荐开启；已识别 Agent 始终归一化 |
| `use_default_keywords` | `true` | 控制旧模式的默认关键词预设，以及启发式的身份线索 |
| `custom_mappings` | 空 | 显式系统文本映射，默认 `{}` |
| `audit` | `false` | 记录请求 ID、正文哈希、Agent 和改写计数；省略提示词原文 |

`agent_alias` 从原始系统文本和实际工具声明识别 Agent。系统文本先应用显式自定义映射，再使用真实工具别名、Agent 片段模板和整词补漏。Claude Code 省略已审查的厂商帮助、模型推荐和产品介绍。Gemini CLI 同时检验身份句、`cli_help` 与 `invoke_agent` 声明，保留非交互模式和帮助工具的用途。Pi 的 `pi agent` 会改写，`api`、`pipeline` 和 `PiAgent` 保留。已声明的 `pi_read` 及其引用使用同一个可逆别名。品牌整词补漏会跳过作普通名词用的小写歧义词，如 `move the cursor`、`a goose`、`your AI copilot`、`cascade of errors`。品牌目录为各家记录厂商名（`vendors`）：只有 `built/created/developed by X`、`a product of X`、`X's official CLI`、`(formerly X)`、`由 X 开发` 这类归属短语会改为 Google Deepmind 团队或删去；`Anthropic API`、`Microsoft content policies` 等功能性引用保留。审计记录模板命中数、残留修正数和保护区命中数。

每个 Agent 通过统一注册入口提供身份规则和可选执行计划。四种协议共用工具声明、Schema、调用与结果视图。OpenCode Code Mode 适配器验证正式目录和 `execute` 的 Schema，支持 `tools.read` 与 `tools.opencode.worktree_list` 这样的单段和双段静态路径。目录引用、`search` 和 `execute.code` 使用同一套映射，客户端执行前恢复原名。动态路径或无法安全还原的调用返回明确错误。

工具声明及参数 Schema 的 `description` 字符串也应用模板和整词补漏。参数名、枚举、默认值、调用参数、普通工具结果、文件内容和历史正文保留。OpenCode 适配器使用固定版本的 JavaScript AST 解析器，按原源码区间改写确定的工具引用。

只支持 `mode: rewrite`。在旧模式中，关闭 `heuristic_identity` 会使用传统关键词替换。已识别 Agent 的模板和整词补漏由 `agent_alias` 控制。

处理范围依据 `selected_auth_index` 查询到的实际提供商，不根据模型名称猜测。插件自行执行且未经过 CPA 认证调度的请求没有该索引，会保持原样；裸模型名可能沿现有路由被其他插件接管。

停用其他具有相同处理功能的旧过滤器，避免叠加改写。单独关闭 `antigravity_bridge_system_prompt` 可恢复原生系统字段位置；关闭对应提供商开关可绕过其改写和搬移。

## 构建与验证

### Agent 版本和请求跟踪

GitHub Actions 的 **Agent traces** 工作流每天马来西亚时间 09:23 运行，也可通过 **Run workflow** 选择单条发行线。它跟踪 OpenCode V1、OpenCode V2、Pi、Codex、Gemini CLI 和 Claude Code 的稳定版。官方客户端在临时 Git 项目中连接隔离的本地模型端点，发出实际的文本及工具请求。OpenCode 另外探测 Code Mode，以实际目录、`execute` 声明和读取回合为证据。[工作流](.github/workflows/agent-traces.yml)

在运行页 Summary 查看相对发布基线的具体差异，并下载 `agent-traces-all`。汇总包保留 30 天，包含各发行线的实际版本 `release.json`、脱敏请求及哈希 `trace.json`、ABI 回放与差异 `report.json`，以及采集失败时可用的诊断信息。规则候选通过代码审查纳入运行时。

从 `v0.5.4` 起，发布 tag 会自动采集六条发行线。采集和回放全部通过后，长期基线 `agent-traces-baseline.zip` 附在该 tag 的 GitHub Release。手动和定时运行比较最新稳定 tag，发布运行比较前一个稳定 tag。缺少基线、读取失败或采集配置不兼容时会在 Summary 标出。安装包继续独立发布。

本地端点返回固定响应，验证客户端实际发送的内容、工具回合和别名恢复。真实模型对提示词改写的理解效果需要单独对照。

### 代码结构

根目录 `main.go` 提供 C ABI 入口。`internal/plugin` 管理 CPA 拦截器、配置快照和请求状态，`internal/config` 定义配置与发布版本。协议字段和桥接布局在 `internal/protocol`。Agent 识别、模板、工具别名及 OpenCode Code Mode 适配在 `internal/rewrite`，新增 Agent 规则从该包的注册表接入。

### 插件构建

构建工具版本固定在 `.go-version` 和 `.zig-version`：Go **1.27.3**、Zig **0.17.1**；ABI 脚本使用 Python 3.12+ 标准库。

```sh
go test -race -count=1 ./...
go vet ./...
sh scripts/build.sh
```

构建结果为 `build/antigravity-prompt-bridge.so`，目标为 Linux amd64、`GOAMD64=v1`、GNU libc 2.34。在 Linux 上执行编译库验证和打包：

```sh
python3 scripts/abi_smoke.py build/antigravity-prompt-bridge.so
python3 scripts/native_bridge_abi.py build/antigravity-prompt-bridge.so --output build/abi-report.json
python3 scripts/package_release.py
python3 scripts/verify_release.py
```

ABI 回归使用仓库内 32 份测试样本，覆盖 2 个提供商、4 个入口、2 个搬移开关状态，共 512 组精确结构检查与 512 组幂等检查。样本由已确认的短身份句和本项目编写的规则组成，详见 [NOTICE](NOTICE.md)。可用 `--corpus /path/to/corpus.json` 指定额外样本，格式为 `{id, text, expected}` 数组。

在 macOS 上可额外构建本机测试库并运行同一组 ABI 检查；该库不是发布产物：

```sh
mkdir -p build
go build -buildmode=c-shared -o build/antigravity-prompt-bridge.dylib .
python3 scripts/abi_smoke.py build/antigravity-prompt-bridge.dylib
python3 scripts/native_bridge_abi.py build/antigravity-prompt-bridge.dylib
```

推送 `main` 或创建 PR 会运行 CI。发布时先同步源码和 `registry.json` 版本，再推送相应的 `vX.Y.Z` 标签；版本检查、Go 测试、ABI 检查和安装包校验全部通过后，工作流发布压缩包与校验文件。

## 已知边界与回退

启发式以英文声明为主，辅以有限中文支持，无法覆盖所有未来模板。超过 256 KiB 的提示词字段不做启发式身份改写；单句上限 2,048 字节，每字段最多改写 128 次。

插件不保证上游额度或可用性。更名前的 v0.3.0 曾在原生 Antigravity + OpenCode 的 Gemini 协议中完成 Flash Lite/Flash High 读文件工具调用；单次成功不代表所有账户和模型长期可用。

回退位置处理可关闭搬移开关。回退二进制时，移出新版本文件，恢复备份，再保存配置触发热加载；不要将多个同 ID 的旧二进制长期留在发现目录。

本项目为 MIT 许可的独立改造版本；原作者、参考项目和测试材料说明见 [NOTICE](NOTICE.md)，完整许可见 [LICENSE](LICENSE)。
