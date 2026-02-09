# AI Agent Worker 任务清单（执行入口）

> **权威指引**：`docs/Argus_OTA_Platform.md`（RAG 搭建章节）  
> **Eino 标准**：`docs/eino_design.md` + `docs/background/*`

## 1. pgvector 检索器 + Embedding 接入（最高优先级）

- [ ] 创建 `internal/infrastructure/pgvector/vector_retriever.go`
- [ ] 创建 `internal/infrastructure/llm/glm4_embedding.go`（或适配 OpenAI）
- [ ] 通过 Eino `model.EmbeddingModel` 标准接口接入
- [ ] RAG 失败必须 `RAGUnavailable=true` 降级

## 2. Kafka Consumer 接入主流程

- [ ] 将 `internal/infrastructure/kafka/consumer.go` 接入主 Worker
- [ ] 消费 `GatheringCompleted` → 触发 `MultiAgentService.DiagnoseBatch`
- [ ] 诊断结果回写 DB + 发布 `DiagnosisCompleted`

## 3. Supervisor/动态路由（路线图）

- [ ] 在 Sequential Graph 上引入 Condition Node
- [ ] 低置信度 → 走 RAG；高置信度 → 快速通道
- [ ] 最终演进为 Supervisor Graph

## 4. SSE 订阅端

- [ ] 已有 Redis Pub/Sub 发布端（`event_publisher.go`）
- [ ] 实现 HTTP SSE 订阅端（Gin Handler）

> 详细步骤、第一性原理与 Eino 标准请以 `docs/Argus_OTA_Platform.md` 为准。
