# AI Agent Worker（当前实现摘要）

**当前实现**：Sequential Graph（RAG Pipeline）  
**权威文档**：`docs/Argus_OTA_Platform.md`  
**Eino 设计**：`docs/eino_design.md`

## 当前状态

- 流程：`DataLoader → RAG → LLM`（Sequential Graph）
- RAG 降级标记：`RAGUnavailable`
- VectorRetriever 仍为 mock（待接入 pgvector + Embedding）

## 你该先看哪里

1. `docs/Argus_OTA_Platform.md` 的 **RAG 搭建章节**
2. `docs/eino_design.md`（Eino 标准与演进）
3. `docs/background/*`（Eino 标准开发参考）

## 待完成清单（执行入口）

详见 `workers/ai-agent/TASKS.md`
