> 🌐 **语言**: [English](README.md) · **简体中文**

# OCTO Matter（简体中文）

> **轻量级任务 / Todo / Matter 服务** — OCTO 平台的子模块。

`octo-matter` 是一个 Go 服务，负责管理 **matter**（任务 / Todo / 行动项）。
它对外暴露 REST API，提供创建 / 修改 / 指派 / 关闭等能力，并通过
timeline 子模块记录每个 matter 上的活动（评论、状态变更、LLM 抽取出的
后续动作）。

## ✨ 特性

- Matter 的 CRUD、指派、可见性控制
- 每个 matter 一条 **timeline**，append-only 活动流
- 可插拔的 **LLM 抽取器**，把自由文本（聊天片段、会议纪要）转为结构化
  matter 草稿
- 抽象化的 **Notifier**，默认对接 OCTO IM 后端，也可以接任意 HTTP
  webhook
- 极小的外部依赖 — MySQL + Redis + 一个 OpenAI 兼容的 LLM endpoint

## 🚀 快速开始

```bash
git clone https://github.com/Mininglamp-AI/octo-matter.git
cd octo-matter
go build ./cmd

# 配置通过环境变量（完整列表见 internal/config/config.go）
export LLM_API_URL=https://api.example.com/v1
export DB_DSN='user:pass@tcp(127.0.0.1:3306)/matter?parseTime=true'

./cmd serve
```

## 🧱 模块结构

| 路径 | 作用 |
|---|---|
| `cmd/` | 服务入口 + timeline 事务辅助 |
| `internal/config/` | 基于环境变量的配置加载 |
| `internal/handler/` | HTTP handler（matter / timeline / activity / extract） |
| `internal/service/` | 业务逻辑（访问控制 / LLM 抽取 / timeline） |
| `internal/repository/` | MySQL 仓储（matter / assignee / channel / timeline） |
| `internal/llm/` | LLM 客户端 — OpenAI 兼容的 `/v1/chat/completions` |
| `internal/notification/` | Notifier 接口 + OCTO-IM 实现 |
| `internal/apperr/` | 类型化应用错误 |
| `internal/model/` | 共享数据模型 |
| `internal/middleware/` | 鉴权 / 日志 / recovery 中间件 |
| `migrations/` | SQL schema 迁移 |

## 🛠️ 生态

[OCTO](https://github.com/Mininglamp-AI) 开源平台的一部分。

## 🤝 贡献

参考 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [行为准则](CODE_OF_CONDUCT.md)。

## 📄 License

Apache License 2.0 — 详见 [LICENSE](LICENSE)。

---

由 OCTO 社区 🐙 共同开发
