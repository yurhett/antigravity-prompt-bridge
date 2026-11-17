# v0.3.1

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
