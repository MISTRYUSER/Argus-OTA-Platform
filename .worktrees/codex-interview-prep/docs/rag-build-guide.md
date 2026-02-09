# RAG 搭建与后续任务指导（Argus OTA Platform）

> **用途**：这份文档是你“按图索骥”的执行手册，重点带你完成 **RAG 搭建**，并教你完成其余未完成项。  
> **标准**：严格遵守 **Eino 开发标准**（`model.ChatModel` / `model.EmbeddingModel` / `tool.Utils.InferTool` / `compose.Chain|Graph` / `RAGUnavailable` 降级）。  
> **权威参考**：`docs/Argus_OTA_Platform.md` 的 **0.9 RAG 搭建**、`docs/eino_design.md`、`docs/background/*`。

---

## 0. 推荐阅读顺序（不要跳）

1. `docs/Argus_OTA_Platform.md`（0.9 RAG 搭建）
2. `docs/background/2.Eino 框架 ai 工具开发文档.md`
3. `docs/background/code_review_feedback.md`
4. `docs/eino_design.md`

---

## 1. 第一性原理：你要怎么想

1. **诊断不是“聪明”，而是“可验证知识”**  
   车辆诊断必须基于历史案例与硬件知识，RAG 是把“事实”放进推理环节。
2. **召回可控比“相似度高”更重要**  
   先硬过滤（车型/错误码/版本）再语义排序，避免跨车型误诊。
3. **降级必须显式**  
   RAG 失败要让 LLM 知道“知识库不可用”，否则会幻觉。
4. **标准化接口优先**  
   不要裸 HTTP，必须使用 Eino 标准接口，保证 Token 统计、Tracing、统一重试。

---

## 2. RAG 搭建（重点）

### 2.1 开启 pgvector

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

存储位置二选一：  
- 方案 A：直接使用 `ai_diagnoses.embedding`  
- 方案 B：新建知识表（按 `docs/Argus_OTA_Platform.md` 0.9 章节）

> 建议：先用方案 A 快速跑通，再考虑独立知识库表。

---

### 2.2 Embedding 接入（必须使用 Eino 标准接口）

**文件**：`workers/ai-agent/internal/infrastructure/llm/glm4_embedding.go`  
**要求**：使用 `model.EmbeddingModel`，禁止裸 HTTP。

设计要点：  
- 统一依赖 Eino 标准接口  
- 返回向量维度必须与数据库向量维度一致  
- 向量是否归一化决定后续相似度算子选择

**代码骨架（示例）**：

```go
package llm

import (
    "context"
    "fmt"

    "github.com/cloudwego/eino/components/embedding"
    "github.com/cloudwego/eino-ext/components/embedding/openai"
)

type EmbeddingProvider struct {
    model embedding.EmbeddingModel
}

func NewEmbeddingProvider(apiKey, modelName string) (*EmbeddingProvider, error) {
    if apiKey == "" {
        return nil, fmt.Errorf("missing api key")
    }
    m, err := openai.NewEmbeddingModel(context.Background(), &openai.EmbeddingModelConfig{
        APIKey: apiKey,
        Model:  modelName,
    })
    if err != nil {
        return nil, err
    }
    return &EmbeddingProvider{model: m}, nil
}

func (p *EmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
    return p.model.Embed(ctx, text)
}
```

> 参考：`docs/background/2.Eino 框架 ai 工具开发文档.md`、`docs/background/code_review_feedback.md`

---

### 2.3 pgvector Retriever 实现

**文件**：`workers/ai-agent/internal/infrastructure/pgvector/vector_retriever.go`  
**SQL 模板**：见 `docs/Argus_OTA_Platform.md` 0.9.4  
**重要**：向量是否归一化决定用 `<=>` 或 `<#>`（见 `code_review_feedback.md`）。

实现逻辑：  
- 用 EmbeddingModel 生成查询向量  
- SQL 先硬过滤，再相似度排序  
- 返回 `SimilarCase` 列表

**SQL 示例**：

```sql
SELECT id, batch_id, diagnosis_summary, confidence,
       1 - (embedding <=> $1::vector) AS similarity
FROM ai_diagnoses
WHERE embedding IS NOT NULL
  AND ($2::text[] IS NULL OR top_error_codes && $2::text[])
ORDER BY embedding <=> $1::vector
LIMIT $3;
```

**归一化判断**：  
- 若向量 **已归一化**：用 `<=>`（Cosine Distance）  
- 若向量 **未归一化**：用 `<#>` 或先归一化再用 `<=>`

---

### 2.4 RAGNode 接入

**文件**：`workers/ai-agent/internal/application/nodes/rag_node.go`  
**要求**：RAG 失败必须 `RAGUnavailable=true`，继续流程。

示例逻辑：

```go
cases, err := retriever.Search(...)
if err != nil {
    input.RAGCases = []domain.SimilarCase{}
    input.RAGUnavailable = true
    return input, fmt.Errorf("RAG failed but continuing: %w", err)
}
input.RAGCases = cases
input.RAGUnavailable = false
```

---

### 2.5 替换 Mock Retriever

**文件**：`workers/ai-agent/cmd/ai-worker/main.go`  
**动作**：用真实 `PgvectorRetriever` 替换 `MockVectorRetriever`。

```go
vectorRetriever, err := pgvector.NewPgvectorRetriever(db, os.Getenv("GLM_API_KEY"))
if err != nil {
    log.Fatalf("failed to create retriever: %v", err)
}
```

---

### 2.6 执行入口

执行入口已写在 `workers/ai-agent/TASKS.md`。  
完整原理与步骤：`docs/Argus_OTA_Platform.md` 的 **0.9 RAG 搭建**章节。

---

## 3. 其余未完成项（教你怎么做）

### 3.1 Kafka 消费者接入主流程

**文件**：`workers/ai-agent/internal/infrastructure/kafka/consumer.go`  
**动作**：接入 `MultiAgentService.DiagnoseBatch`。  
**结果**：诊断完成后发布 `DiagnosisCompleted` 事件。

步骤要点：  
- 消费 `GatheringCompleted`  
- 解析 batch_id  
- 调用 `DiagnoseBatch`  
- 诊断结果保存 + 发布事件

---

### 3.2 Supervisor / 动态路由

**参考设计**：`docs/eino_design.md` v2.0  

步骤要点：  
- 在 Sequential Graph 上引入 Condition Node  
- 低置信度 → 走 RAG  
- 高置信度 → 快速通道

---

### 3.3 SSE 订阅端

**现状**：发布端已有 `event_publisher.go`  
**你要做的事**：新增 HTTP SSE Handler（Gin）订阅 `batch:{id}:progress`。

步骤要点：  
- Gin Handler 内部启动 `redis.Subscribe`  
- 设置 `Content-Type: text/event-stream`  
- 持续写入事件流

---

## 4. 面试题（可能会问什么）

**Q1：为什么 RAG 必须做硬过滤？**  
A：相似度是软约束，车型/版本是硬约束，否则会跨车型误诊。

**Q2：为什么要显式 `RAGUnavailable`？**  
A：让模型知道“检索失败”，避免把“没检索到”当成“没有相似案例”。

**Q3：为什么必须使用 Eino 的 Model 接口？**  
A：获得 Token 统计、Tracing、统一重试与熔断，避免裸 HTTP 失控。

**Q4：向量相似度用 `<=>` 还是 `<#>`？**  
A：看向量是否归一化。归一化用 `<=>`，未归一化用 `<#>` 或先归一化。

**Q5：为什么先落地 Sequential Graph？**  
A：先保证链路可跑、可回放，再演进到 Supervisor。

---

## 5. 你应该怎么设计、怎么写

设计原则：  
- 标准化接口优先  
- 失败必须降级  
- 保证可观测性（Token/Tracing/日志）  
- 每个阶段可验证

避免做法：  
- 裸 HTTP 调 LLM/Embedding  
- 只靠向量相似度不做硬过滤  
- RAG 失败直接返回空列表

---

## 6. 验收清单（完成后自检）

- pgvector 可用  
- Embedding 生成成功  
- Retriever 能返回 TopK  
- RAG 失败时 LLM 走降级  
- Sequential Graph 可完整跑通

---

## 7. 入口索引

- 任务执行入口：`workers/ai-agent/TASKS.md`  
- 背景知识：`docs/background/*`  
- Eino 设计：`docs/eino_design.md`  
- 权威流程：`docs/Argus_OTA_Platform.md` 的 0.9 章节
