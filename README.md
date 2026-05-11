<p align="center">
  <img src="./docs/assets/logo-light.png" width="200" alt="OCTO">
</p>

<p align="center">
  <b>OCTO — 为人和 AI Agent 协作而生的开源工作平台</b><br/>
  <sub>让 <b>龙虾（Lobster / OpenClaw-powered digital double agents）</b> 去「思」和「行」，让人聚焦在「品」</sub>
</p>

<p align="center">
  <a href="https://github.com/Mininglamp-OSS/octo-server#-what-is-octo"><b>📖 产品 Overview · What is OCTO</b></a> ·
  <a href="#-quickstart"><b>🚀 Quickstart</b></a> ·
  <a href="#-octo-ecosystem"><b>📦 Ecosystem</b></a> ·
  <a href="./CONTRIBUTING.md"><b>🤝 Contributing</b></a>
</p>

---

> 🌐 **Read in**: **English** · [简体中文](README.zh.md)

# OCTO Matter

> **Lightweight task / todo / "matter" service** for the OCTO platform.

`octo-matter` is a Go service that manages **matters** — a generalised
task / todo / action-item primitive used by the OCTO clients and by
Lobster agents. It exposes a REST API for create / update / assign /
close, plus a timeline sub-service that records activity against each
matter (comments, status changes, LLM-extracted follow-ups).

## ✨ Features

- CRUD + assignment + visibility controls for matters
- Per-matter **timeline** with append-only activity records
- Pluggable **LLM extractor** that turns free-form text (chat snippets,
  meeting notes) into structured matter drafts
- **Notifier** abstraction for integrating with the OCTO IM backend
  (default) or any HTTP webhook sink
- Minimal external dependencies — MySQL + Redis + an LLM endpoint

## 🚀 Quickstart

```bash
git clone https://github.com/Mininglamp-OSS/octo-matter.git
cd octo-matter
go build ./cmd

# configure via env vars (see internal/config/config.go for the full list)
export LLM_API_URL=https://api.example.com/v1
export DB_DSN='user:pass@tcp(127.0.0.1:3306)/matter?parseTime=true'

./cmd serve
```

## 🧱 Modules

Top-level packages under this module:

| Path | Purpose |
|---|---|
| `cmd/` | Service entrypoint + timeline transaction helper |
| `internal/config/` | Env-driven config loader |
| `internal/handler/` | HTTP handlers (matter, timeline, activity, extract) |
| `internal/service/` | Business logic (access control, LLM extraction, timeline) |
| `internal/repository/` | MySQL repositories for matters, assignees, channels, timelines |
| `internal/llm/` | LLM client — OpenAI-compatible `/v1/chat/completions` |
| `internal/notification/` | Notifier interface + OCTO-IM implementation |
| `internal/apperr/` | Typed application errors |
| `internal/model/` | Shared data models |
| `internal/middleware/` | Auth / logging / recovery middleware |
| `migrations/` | SQL schema migrations |

## 🛠️ Ecosystem

Part of the [OCTO](https://github.com/Mininglamp-OSS) open-source platform.

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Please also read our [Code of Conduct](CODE_OF_CONDUCT.md).

## 📄 License

Apache License 2.0 — see [LICENSE](LICENSE).

---

Made with 🐙 by the OCTO community.

---
<!-- Shared snippet: OCTO repo matrix. Keep identical across all 8 repos. -->

## 📦 OCTO Ecosystem

```mermaid
graph TD
  subgraph Clients[Clients]
    Web[octo-web<br/>Web / PC]
    Android[octo-android]
    iOS[octo-ios]
  end

  subgraph Core[Core Services]
    Server[octo-server<br/>Backend API]
    Matter[octo-matter<br/>Task / Matter Service]
    IM[WuKongIM<br/>IM Engine]
  end

  subgraph Libs[Libraries & Tools]
    Lib[octo-lib<br/>Core Library]
    CLI[octo-daemon-cli<br/>CLI Tools]
    Adapters[octo-adapters<br/>Third-party Integrations]
  end

  Web --> Server
  Android --> Server
  iOS --> Server
  Server --> IM
  Server --> Matter
  Server --> Adapters
  Server -.uses.-> Lib
  Matter -.uses.-> Lib
  CLI -.uses.-> Lib
  Adapters -.uses.-> Lib
```

| Repo | Language | Purpose | Status |
|------|----------|---------|--------|
| [`octo-server`](https://github.com/Mininglamp-OSS/octo-server) | Go | Backend API, orchestration, Lobster agent dispatch | Public |
| [`octo-matter`](https://github.com/Mininglamp-OSS/octo-matter) | Go | Task / Todo / Matter service | Public |
| [`octo-web`](https://github.com/Mininglamp-OSS/octo-web) | TypeScript / React | Web & PC client | Public |
| [`octo-android`](https://github.com/Mininglamp-OSS/octo-android) | Kotlin / Java | Android client | Public |
| [`octo-ios`](https://github.com/Mininglamp-OSS/octo-ios) | Swift / Objective-C | iOS client | Public |
| [`octo-lib`](https://github.com/Mininglamp-OSS/octo-lib) | Go | Core domain models / protocols / utilities | Public |
| [`octo-daemon-cli`](https://github.com/Mininglamp-OSS/octo-daemon-cli) | Go | Ops & onboarding CLI, local daemon | Public |
| [`octo-adapters`](https://github.com/Mininglamp-OSS/octo-adapters) | TypeScript / Python | Third-party integrations (IM / SSO / storage) | Public |
| [`WuKongIM`](https://github.com/Mininglamp-OSS/WuKongIM) | Go | IM engine (fork, enhanced for OCTO) | Public |
