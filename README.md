# antigravity-prompt-bridge

[![Release](https://img.shields.io/github/v/release/yurhett/antigravity-prompt-bridge)](https://github.com/yurhett/antigravity-prompt-bridge/releases/latest)

[English](README.en.md) · [配置示例](examples/config.yaml) · [更新日志](CHANGELOG.md) · [来源与许可](NOTICE.md)

CLIProxyAPI（CPA）原生 ABI v1 插件，为 `agy` 和原生 `antigravity` 提供提示词处理。

- 最新公开版本：**v0.5.4**（Linux amd64 动态库）。下文的 `brand_guard` 和 `unidentified` 适用于 v0.5.5。
- 仓库名、插件 ID、商店名称、配置键、发布包前缀统一为 **`antigravity-prompt-bridge`**

## 目录

- [功能](#功能)
- [快速开始](#快速开始)
- [安装](#安装)
- [配置](#配置)
- [工作原理](#工作原理)
- [升级与回退](#升级与回退)
- [已知边界](#已知边界)
- [许可](#许可)

## 功能

| 能力 | 说明 |
|---|---|
| Agent 提示词改写 | 从自我声明识别 Agent，先应用片段模板，再按整词修正残留品牌；可启用整句身份归一化 |
| 原生提示词桥接 | 仅对原生 `antigravity` 生效：先改写，再把调用方 system/developer 提示词放进用户消息的 `<USER_REQUEST>` 包装 |
| 独立开关 | `agy` 与 `antigravity` 可分别停用；搬移开关默认开启，不改变 `agy` 原有处理方式 |
| 协议适配 | Chat Completions、Responses、Anthropic Messages、Gemini 及 Antigravity `request` 外层；工具名可逆改写 |

## 快速开始

1. 在 CPA 主配置中添加插件源：

   ```yaml
   plugins:
     enabled: true
     store-sources:
       - https://raw.githubusercontent.com/yurhett/antigravity-prompt-bridge/main/registry.json
   ```

2. 刷新商店，安装 **antigravity-prompt-bridge**（ID：`antigravity-prompt-bridge`）。
3. 合并[推荐配置](#推荐配置)，保存后触发热加载。
4. 停用功能相同的旧过滤器（如 `agy-coding-filter`、`antigravity-coding-filter`），避免叠加改写。

## 安装

### 环境要求

- Linux x86-64，GNU libc **2.34+**（不支持 Alpine/musl）
- 支持原生 ABI v1、请求拦截器和 `host.auth.get_runtime` 的 CPA
- 当前依赖 CPA SDK `v7.3.2`

### 方式一：CPA 自定义插件源（推荐）

见[快速开始](#快速开始)。请使用本项目的源，确保仓库地址和插件 ID 对应。

### 方式二：手动安装

1. 从 [Releases](https://github.com/yurhett/antigravity-prompt-bridge/releases) 下载 `antigravity-prompt-bridge_0.5.4_linux_amd64.zip` 和 `checksums.txt`。
2. 校验并解压：

   ```sh
   sha256sum -c checksums.txt
   unzip antigravity-prompt-bridge_0.5.4_linux_amd64.zip
   ```

3. 将 `antigravity-prompt-bridge.so` 放入 CPA 插件发现目录，建议命名为 `antigravity-prompt-bridge-v0.5.4.so`；旧版本备份放到发现目录以外。
4. 保存插件配置触发热加载，并确认已加载版本为 `0.5.4`。

## 配置

### 推荐配置

合并到现有 `plugins.configs`，保留其他插件设置：

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

### 配置项

| 字段 | 默认值 | 说明 |
|---|---|---|
| `mode` | `rewrite` | 目前仅支持 `rewrite` |
| `enable_agy` | `true` | 对实际选中的 agy 认证启用处理 |
| `enable_antigravity` | `true` | 对实际选中的原生 Antigravity 认证启用处理 |
| `antigravity_bridge_system_prompt` | `true` | 原生 Antigravity 先改写再搬移；不影响 agy。关闭后恢复原生系统字段位置 |
| `agent_alias` | `true` | 识别成功后启用标准身份句、模板、品牌整词补漏与工具别名。未识别时按 `unidentified` 处理；关闭后所有请求走旧模式 |
| `heuristic_identity` | `false`（推荐开） | 未识别请求的启发式整句身份归一化；已识别 Agent 始终归一化。旧模式下关闭则用传统关键词替换 |
| `brand_guard` | `reject` | 已识别 Agent 的规则没命中时怎么处理：`off` / `report` / `reject` / `strict`，见下文 |
| `unidentified` | `reject` | 识别不出 Agent 时的策略：`reject` / `legacy`，见下文 |
| `use_default_keywords` | `true` | 旧模式默认关键词预设与启发式身份线索 |
| `custom_mappings` | `{}` | 显式系统文本映射，优先于其他规则 |
| `audit` | `false` | 记录请求 ID、正文哈希、Agent、模板命中数、残留修正数、保护区命中数；不记录提示词原文 |

## 工作原理

### 处理范围

依据 `selected_auth_index` 查询到的实际提供商决定是否处理，不根据模型名猜测。插件自行执行、未经 CPA 认证调度的请求没有该索引，保持原样；裸模型名可能沿现有路由被其他插件接管。

改写对象包括 system/developer 消息、Responses 的 `instructions` 和 Gemini 的 `systemInstruction`。启发式改写会保护以下内容，不做修改：

- 引号内的示例、Markdown 引用与链接
- 代码、路径和 URL
- 已识别的 XML 项目块、示例块和不受信任内容块

独立的行为约束子句保留；含义有歧义的内嵌限制会跳过。显式 `custom_mappings` 在现有映射保护规则内优先生效。支持的片段模板及其来源记录在 `internal/rewrite/assets/agent-prompt-templates.json`。

### 桥接布局

身份句：

> You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.

```text
系统字段：Antigravity 原版 <identity> 块

新增用户消息：
  <USER_REQUEST>
  Follow the system and developer instructions ...
  --- message 1 role=system ---
  已改写身份句的调用方系统提示词，其余正文保留
  </USER_REQUEST>

原有用户问题、助手消息和工具结果：按原顺序保留
```

- 包装内的 `role=system/developer` 只是文本标签，内容实际属于用户消息。
- 不移植官方 CLI 的工具、目录或权限说明。
- 含不支持的非文本系统块时保留原位置；重复处理不会重复包装。

### Agent 识别与改写

`agent_alias` 从原始系统文本和实际工具声明识别 Agent。处理顺序如下：

1. 显式 `custom_mappings`
2. 真实工具别名
3. Agent 片段模板
4. 品牌整词补漏

| Agent | 规则 |
|---|---|
| Claude Code | 省略已审查的厂商帮助、模型推荐和产品介绍 |
| Gemini CLI | 同时检验身份句、`cli_help` 与 `invoke_agent` 声明；保留非交互模式和帮助工具用途 |
| Pi | 改写 `pi agent`，保留 `api`、`pipeline`、`PiAgent`；`pi_read` 及其引用使用同一可逆别名 |
| OpenCode | 支持 Code Mode，见下文 |

品牌补漏规则：

- 跳过当作普通名词使用的小写歧义词，如 `move the cursor`、`a goose`、`your AI copilot`、`cascade of errors`。
- 只改写归属短语（`built/created/developed by X`、`a product of X`、`X's official CLI`、`(formerly X)`、`由 X 开发`），改为 Google Deepmind 团队或删去。
- 保留功能性引用，如 `Anthropic API`、`Microsoft content policies`。

### 规则覆盖（`brand_guard`）

适配器可能跟不上客户端更新，漏改本该改的地方。`brand_guard` 统计每条规则的命中次数，不扫描提示词内容，所以项目说明、路径、命令这类规则有意保留的内容不会被算作问题。已识别的 Agent 必须命中：

| 规则 | 级别 | 含义 |
|---|---|---|
| `identity:<Agent>` | 缺口 | 自我介绍句已替换为 Antigravity 标准身份句，模板或身份归一化都算 |
| `identity-template:<Agent>` | 漂移 | 该 Agent 有已记录的身份模板时，至少命中一个。没命中说明客户端改了身份句，但身份归一化已经替换了它 |
| `adapter:<适配器>` | 缺口 | 请求声明了适配器负责的功能时，适配器必须生效。例如声明了 `execute(code: string)`，OpenCode Code Mode 就必须生效 |
| `adapter:<适配器>/<子规则>` | 缺口 | 适配器自己声明的规则，例如 `adapter:opencode-code-mode/group-labels`：目录里每个分组名都随路径一起改写 |

缺口会把 Agent 特征发往上游；漂移已经被兜住，只说明模板该更新了。

| 值 | 缺口 | 漂移 | 适合场景 |
|---|---|---|---|
| `off` | 不处理 | 不处理 | — |
| `report` | 记 warn 日志，照常转发 | 记日志 | 观察 |
| `reject`（默认） | 返回 400 `agent_bridge_coverage_gap`，不发上游 | 记日志 | 日常使用 |
| `strict` | 返回 400 | 返回 400 | 开发、调适配器 |

400 响应的 `error.coverage` 列出每条规则的级别、命中数和期望数。日志不含提示词原文。

### 未识别请求（`unidentified`）

开启 `agent_alias` 但识别不出 Agent 时：

| 值 | 行为 |
|---|---|
| `reject`（默认） | 按 Agent 指纹判断：带有某个已知 Agent 的全部指纹特征时，返回 400 `agent_bridge_unidentified`，不发上游；否则只补上 Antigravity 标准身份句，其余原样转发 |
| `legacy` | 走旧模式的关键词替换或启发式改写（见 `heuristic_identity`），不加身份句 |

**指纹**：每个已知 Agent 记录两个高度特有的特征，取自真实请求，见 `internal/rewrite/assets/agent-fingerprints.json`。特征是系统指令里的一段文字，或带指定参数的工具声明；用户消息不参与判断。两个都命中，说明这是该 Agent 的请求，只是身份句变了、没被识别出来，转发出去就会带着它的特征，所以拒绝并提示更新插件。只命中一个时照常转发，并记一条 warn 日志。目前覆盖 OpenCode、Codex、Claude Code、Gemini CLI 和 Pi。

**身份句**：有系统或开发者指令时加在第一段前面；已经以标准身份句开头的不再重复。没有指令时，按 Gemini、OpenAI Chat、Responses 或 Anthropic 的格式新建；无法判断格式时不加。

关闭 `agent_alias` 后，所有请求都走旧模式，不做指纹判断，也不加身份句。

### 工具别名

- 工具声明及参数 Schema 的 `description` 同样应用模板与补漏。
- 参数名、枚举、默认值、调用参数、普通工具结果、文件内容和历史正文保持不变。
- 别名可逆：客户端执行工具前恢复原名。
- **OpenCode Code Mode**：支持 `tools.read`、`tools.opencode.worktree_list` 这类静态路径；目录引用、`search` 和 `execute.code` 共用一套映射。动态路径或无法安全还原的调用会返回明确错误。

## 升级与回退

### 从旧名称迁移（v0.3.0 → v0.3.1+）

1. 将 `plugins.configs.agy-coding-filter` 的配置原样迁到 `plugins.configs.antigravity-prompt-bridge`。
2. 移除旧配置键，以及发现目录中的旧动态库。
3. 刷新自定义源，安装新插件，确认只有一个版本生效。
4. 停用功能相同的旧 `antigravity-coding-filter`。

更名不改变处理算法和默认开关。旧 v0.3.0 标签与 Release 保留，供回退使用。

### 回退

- **只回退桥接位置**：关闭 `antigravity_bridge_system_prompt`。
- **绕过某个提供商**：关闭 `enable_agy` 或 `enable_antigravity`。
- **回退二进制**：移出新版本文件，恢复备份，保存配置触发热加载。不要在发现目录长期留着多个同 ID 的二进制。

## 已知边界

- 启发式以英文声明为主，中文支持有限，无法覆盖所有未来模板。
- 超过 256 KiB 的提示词字段不做启发式身份改写；单句上限 2,048 字节，每字段最多改写 128 次。
- 插件不保证上游额度或可用性。

## 许可

MIT 许可的独立改造版本。原作者、参考项目和测试材料说明见 [NOTICE](NOTICE.md)，完整许可见 [LICENSE](LICENSE)。
