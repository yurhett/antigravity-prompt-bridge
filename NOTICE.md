# 来源与许可

本项目基于 [jellyfish-p/cpa-plugin-antigravity-coding-filter](https://github.com/jellyfish-p/cpa-plugin-antigravity-coding-filter) v0.3.1（MIT）改造，保留原作者 jellyfish 的版权与 LICENSE。

本仓库由 yurhett 维护，新增/整理了独立提供商开关、启发式身份识别、原生 Antigravity 提示词桥接、跨协议测试与发布流程。

桥接布局参考 [adeebahmad01/cliproxy-antigravity](https://github.com/adeebahmad01/cliproxy-antigravity) v0.3.1 和官方 CLI 的实际生成记录；本项目不包含官方 CLI 二进制或运行数据。

CLIProxyAPI 通过 Go module 引用，依赖许可证以相应项目为准。本项目不是 Google、Antigravity 或 CLIProxyAPI 官方发行版。

`internal/rewrite/assets/agent-prompt-templates.json` 收录少量运行时替换片段与来源。Codex 条目对应仓库的历史身份样本，其余首批条目对应 `testdata/agent-tool-sources.json` 中的已采集短摘录。`testdata/prompt-corpus.json` 将短身份句与本项目编写的规则及引用场景组合成 32 份测试样本。实际用户提示词、OAuth 凭据与生产日志不属于本仓库内容。

`internal/rewrite/assets/agent-adapter-traces.json` 收录官方客户端在合成项目中实际发出的最小脱敏片段。各条记录包含发行版本、来源、采集场景、字段路径和原采集请求的 SHA-256。
