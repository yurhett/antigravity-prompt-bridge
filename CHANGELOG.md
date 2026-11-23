# 更新记录

## v0.5.4

- 拆分 C ABI 入口、CPA 插件生命周期、配置、协议视图和改写引擎，保留原有插件接口与处理行为。
- Agent traces 随发布 tag 保存基线。手动和定时运行比较最新的稳定 tag，Actions Summary 显示具体差异并提供统一下载包。

## v0.5.3

- 五个已采集 Agent 通过统一注册入口和四协议视图接入。补齐 Claude Code 和 Gemini CLI 识别，按实测模板删减 Claude 厂商说明，保留 OpenCode V1 的继续执行指令。
- OpenCode Code Mode 的单段、双段和已验证的搜索目录路径共享别名和恢复算法。
- 新增 Agent traces 工作流，定时采集六条发行线的官方客户端请求，输出 ABI 回放、版本差异和候选筛选规则。
- 从原始 system/developer 自我声明识别 Agent。对已识别请求先匹配有来源的片段模板，再按整词修正本 Agent 的品牌残留。未识别请求沿用旧模式。
- 工具声明、系统文本和 Schema 描述共享实际的工具别名。保留第三方品牌散文、命令、路径、未声明的工具引用和参数契约。OpenCode Code Mode 继续使用适配器完成目录、调用与回包往返。
- 已识别 Agent 的自我介绍句始终替换为 Antigravity 标准身份句，不再只替换品牌词而留下“Anthropic's official CLI”等厂商描述；不依赖 `heuristic_identity`。修复中文句号后紧接品牌词导致残留泄漏。
- 品牌整词补漏不再替换作普通名词用的小写歧义词（`the cursor`、`a goose`、`your AI copilot`、`cascade of errors`）。
- 品牌目录新增 `Copilot`、`ClaudeCode` 别名和各家 `vendors`。已识别 Agent 的厂商归属短语改为 Google Deepmind 团队或删去，功能性厂商引用保留。
- 按 Codex 0.171.0 实测请求删除 “Within this context, Codex refers to …” 区分句，避免改名后描述出不存在的模型；整句独占一行时连同多余空行一起删除。
- 无空行引导的缩进内容不再按 Markdown 缩进代码块保护，`<available_skills>` 中的技能描述可正常改写；`<id>`、`<name>`、`<location>`、`<path>`、`<file>` 等单行标识元素作为查找键保留。
- 保留 OpenCode 内置技能的实际 ID、显示名和 Code Mode 的 `OPENCODE_SESSION_ID` 环境变量名。
- 品牌替换时同步修正不定冠词（`a Codex skill` → `an Antigravity skill`）。
- 审计增加模板命中、残留修正和保护区命中计数，省略提示词原文。
- 传统关键词模式不再包含 Pi、Roo、Cascade，修复 `Anthropic`、`room`、`cascade` 等普通词被子串替换。
- 删除基于前后缀正则的品牌语境补丁。

## v0.5.2

- 新增公共 AgentAdapter 接口，首个适配器覆盖 OpenCode Code Mode；共用 agent_alias 开关。
- 系统文本中的工具完整名称不再被点号路径保护漏过；真实路径、URL 和插件标识保留。
- 原生 namespace 以及 Code Mode 静态调用名称、search namespace 和已证明来源的目录路径支持精确往返。
- JavaScript AST 只修改执行名称的源码区间，保护普通参数、字符串、注释及文件内容；动态调用无法安全恢复时明确报错。

## v0.5.1

- Agent 别名改为 JSON 字段白名单，仅处理 system/developer 文本与原生工具名称。
- 工具说明、Schema、参数、结果、文件内容和命名空间原样保留；移除数据内容的路径/枚举/参数恢复。
- 插件固定路径保持原文，修复读取源码和品牌清单时被错误判定命名空间或路径冲突。

## v0.5.0

- 统一默认开启的 `agent_alias`，移除 OpenCode 独立配置与实现分支。
- 公共品牌目录覆盖 59 个名称线索，歧义词结合上下文，保留真正用户正文。
- 按工具及参数位置恢复别名，支持 Gemini、OpenAI Chat/Responses、Anthropic 流式回包与原生 freeform 工具；冲突明确拒绝。
- 增加官方工具提示词短摘录、来源调查、协议 ABI 与流分片回归。

## v0.3.1

- 将仓库、运行时插件 ID、商店名称、配置键及发布包前缀统一为 `antigravity-prompt-bridge`。
- 更新 Go 模块路径、安装源、构建脚本、ABI 元数据检查及中英文文档。
- 提示词处理算法、提供商开关和原生搬移默认值保持不变。
- 从 v0.3.0 升级需将 `plugins.configs.agy-coding-filter` 迁移到 `plugins.configs.antigravity-prompt-bridge`，并移出旧二进制，避免重复处理；详见 README。

安装资源：`antigravity-prompt-bridge_0.3.1_linux_amd64.zip` 与 `checksums.txt`。

# v0.3.0

- 支持 `agy` 与原生 `antigravity` 独立启停，保留运行时 ID `agy-coding-filter`。
- 启发式识别并改写身份/运行环境声明，保留规则、代码示例和项目正文。
- 原生 Antigravity 新增默认开启的 `antigravity_bridge_system_prompt`：先改写，再将系统提示词包装为用户输入；`agy` 维持原有处理方式。
- 支持 Chat Completions、Responses、Anthropic Messages 和 Gemini 请求结构，保留工具定义及调用历史。
- 提供独立 ABI 回归测试、Linux amd64 / glibc 2.34 构建、自定义 CPA 插件源和安装包校验。

安装资源：`agy-coding-filter_0.3.0_linux_amd64.zip` 与 `checksums.txt`。

推荐启用 `heuristic_identity: true`，保持 `custom_mappings: {}`。启发式开关的代码默认值为 `false`，以兼容旧配置；示例明确开启。
