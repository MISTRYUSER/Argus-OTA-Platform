# Repo Alignment + RAG Buildout Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 完成 Query Service 的真实 ReportRepo，清理全仓 docs 冗余并保留 `docs/eino_design.md` 与 `LEARNING_LOG.md`，同时补充 RAG 搭建与完成路径的权威说明。

**Architecture:** 以 `docs/Argus_OTA_Platform.md` 作为单一事实源，其他文档仅保留指向与必要差异；ReportRepo 使用 PostgreSQL `reports` 表，存储 Report 的 JSONB；RAG 指导严格遵循 Eino 标准（Model/Tool 接口、降级、可观测）。

**Tech Stack:** Go, PostgreSQL (reports JSONB), Redis, Kafka, Eino v0.7.28, pgvector.

---

### Task 1: 为 ReportRepo 编写失败测试（TDD）

**Files:**
- Create: `internal/infrastructure/postgres/report_repository_test.go`
- Modify: `go.mod`, `go.sum`

**Step 1: 写失败测试（sqlmock）**

```go
func TestReportRepository_Save_Insert(t *testing.T) { /* ... */ }
func TestReportRepository_Save_Update(t *testing.T) { /* ... */ }
func TestReportRepository_FindByID(t *testing.T) { /* ... */ }
func TestReportRepository_FindByBatchID(t *testing.T) { /* ... */ }
```

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/infrastructure/postgres -run TestReportRepository -v`
Expected: FAIL (ReportRepository 未实现)

**Step 3: 处理依赖**

- 如下载失败，先设置：`export GOPROXY=https://proxy.golang.org,direct`
- 再运行：`go mod tidy`

**Step 4: Commit**

```bash
git add go.mod go.sum internal/infrastructure/postgres/report_repository_test.go
git commit -m "test: add report repository sqlmock tests"
```

---

### Task 2: 实现 PostgresReportRepository

**Files:**
- Create: `internal/infrastructure/postgres/report_repository.go`

**Step 1: 最小实现**

- Save:
  - 先用 `batch_id` 查询已有记录
  - 有则 `UPDATE reports SET report_data=..., report_type=...`
  - 无则 `INSERT`
- FindByID/FindByBatchID:
  - 读取 `report_data` JSONB 并反序列化为 `domain.Report`

**Step 2: 运行测试**

Run: `go test ./internal/infrastructure/postgres -run TestReportRepository -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/infrastructure/postgres/report_repository.go
git commit -m "feat: add postgres report repository"
```

---

### Task 3: Query Service 接入真实 ReportRepo

**Files:**
- Modify: `cmd/query-service/main.go`

**Step 1: 替换 mock 实现**

- `reportRepo := postgres.NewPostgresReportRepository(db)`
- 删除 `mockReportRepository`

**Step 2: 基础编译验证**

Run: `go test ./cmd/query-service -run TestNonexistent -v`
Expected: PASS or no tests

**Step 3: Commit**

```bash
git add cmd/query-service/main.go
git commit -m "feat: wire postgres report repository"
```

---

### Task 4: 全仓 docs 清理（单一事实源）

**Files:**
- Modify: `docs/README.md`
- Modify: `docs/architecture-diagram.md`
- Modify: `docs/ai-agent-architecture.md`
- Modify: `docs/getting-started.md`
- Modify: `docs/REMAINING_WORK.md`
- Modify: `docs/development-log.md.backup`
- Keep as-is: `docs/Argus_OTA_Platform.md`, `docs/eino_design.md`, `LEARNING_LOG.md`, `docs/background/*`

**Step 1: 统一文档入口**

- `docs/README.md` 仅保留索引：
  - 权威文档：`docs/Argus_OTA_Platform.md`
  - Eino 设计：`docs/eino_design.md`
  - 学习日志：`LEARNING_LOG.md`
  - 背景知识：`docs/background/*`

**Step 2: 其他文档降级为“指向 + 差异”**

- 将重复内容删除，仅保留：
  - 本文存在的唯一价值（如：图表/示意）
  - 其他内容请参见权威文档

**Step 3: Commit**

```bash
git add docs/README.md docs/architecture-diagram.md docs/ai-agent-architecture.md docs/getting-started.md docs/REMAINING_WORK.md docs/development-log.md.backup
git commit -m "docs: consolidate to single source of truth"
```

---

### Task 5: 在权威文档中补充 RAG 搭建与执行指南（Eino 标准）

**Files:**
- Modify: `docs/Argus_OTA_Platform.md`
- Modify (reduce duplication): `workers/ai-agent/README.md`
- Modify: `workers/ai-agent/TASKS.md`

**Step 1: 添加“RAG 搭建（第一性原理 + Eino 标准）”章节**

必须覆盖：
- 为什么要 RAG（减少幻觉、可控召回）
- 数据流：数据聚合 → Embedding → 混合检索 → LLM
- Eino 标准：
  - 使用 `model.ChatModel` / `model.EmbeddingModel`
  - 工具用 `tool.Utils.InferTool`
  - RAG 失败用 `RAGUnavailable` 降级
  - JSON Mode / Token 统计 / Tracing
- pgvector 启用与 SQL 示例（`<=>` or `<#>` 依据向量归一化）

**Step 2: TASKS.md 对齐**

- 指向 Argus_OTA_Platform 的 RAG 章节
- 仅保留“执行清单”，删掉重复说明

**Step 3: README.md 对齐**

- 保留最短摘要 + 指向 `docs/eino_design.md` 与权威文档

**Step 4: Commit**

```bash
git add docs/Argus_OTA_Platform.md workers/ai-agent/README.md workers/ai-agent/TASKS.md
git commit -m "docs: add RAG build guide and align ai-agent docs"
```

---

### Task 6: 验证

**Step 1: 文档一致性自检**

- 确认 `docs/README.md` 指向正确
- 确认权威文档和 `docs/eino_design.md` 没有互相冲突

**Step 2: Go 测试**

Run: `go test ./internal/infrastructure/postgres -run TestReportRepository -v`
Expected: PASS

**Step 3: Commit**

```bash
git status -sb
```

---

## Execution Handoff

Plan complete and saved to `docs/plans/2026-02-04-repo-alignment-rag-implementation.md`.

Two execution options:

1. Subagent-Driven (this session) — fresh subagent per task, review between tasks
2. Parallel Session (separate) — open new session with executing-plans, batch execution with checkpoints

Which approach?
