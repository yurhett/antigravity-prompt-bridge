# 来源与许可

本项目基于 [jellyfish-p/cpa-plugin-antigravity-coding-filter](https://github.com/jellyfish-p/cpa-plugin-antigravity-coding-filter) v0.3.1（MIT）改造，保留原作者 jellyfish 的版权与 LICENSE。

本仓库由 yurhett 维护，新增/整理了独立提供商开关、启发式身份识别、原生 Antigravity 提示词桥接、跨协议测试与发布流程。

桥接布局参考 [adeebahmad01/cliproxy-antigravity](https://github.com/adeebahmad01/cliproxy-antigravity) v0.3.1 和官方 CLI 的实际生成记录；本项目不包含官方 CLI 二进制或运行数据。

CLIProxyAPI 通过 Go module 引用，依赖许可证以相应项目为准。本项目不是 Google、Antigravity 或 CLIProxyAPI 官方发行版。

`testdata/verified-identities.json` 的短身份句仅用于回归测试，不作为运行时替换词表。`testdata/prompt-corpus.json` 将这些短句与本项目编写的规则及引用场景组合，包含 32 份测试样本；它们不是各 agent 的完整官方系统提示词。实际用户提示词、OAuth 凭据与生产日志不属于本仓库内容。
