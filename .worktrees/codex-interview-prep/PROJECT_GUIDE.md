# 🚀 Argus OTA Platform - 项目快速指南

## 📖 如何使用这个项目

### 1. 首次阅读？从这里开始

**必读文档**：`LEARNING_LOG.md`（项目根目录）

这是项目的**唯一学习日志**，包含：
- ✅ 9 天完整开发记录
- ✅ 40+ 面试高频考点（带标准答案）
- ✅ 27+ Bug 修复经验
- ✅ 关键设计决策与架构理解
- ✅ 系统完整度：75-80%

### 2. 项目状态

**当前进度**：75-80% ⬆️

**已完成**：
- ✅ Ingestor Service（文件上传 + Kafka 发布）
- ✅ Orchestrator Service（Kafka 消费 + 状态机 + Redis Barrier + 补偿任务）
- ✅ Mock Worker（文件解析 + Kafka 发布）
- ✅ 完整状态转换（pending → uploaded → scattering → scattered → gathering → gathered → completed）
- ✅ Kafka DLQ 死信队列
- ✅ 补偿任务（超时恢复机制）

**待完成**（优先级排序）：
1. ⭐⭐⭐ **Query Service**（Singleflight + Redis 缓存）- 用户查询入口
2. ⭐⭐ **AI Agent Worker**（RAG + LLM）- 智能诊断
3. ⭐ **SSE 实时进度**（Server-Sent Events）- 用户体验

### 3. 架构理解

**Worker 流程**：
```
rec 文件 → C++ 解析 → CSV → Python 统计 → PNG/JPG → AI Agent 诊断 → 报告
```

**技术栈**：
- Go (Gin, Sarama, go-redis, pgx)
- PostgreSQL (pgvector)
- Kafka (Redpanda)
- Redis (Barrier 计数)
- MinIO (对象存储)
- Python (Pandas, Matplotlib)
- C++ (高性能解析)

**架构模式**：
- DDD (Domain-Driven Design)
- Event-Driven (Kafka)
- CQRS (Command Query Responsibility Segregation)

### 4. 快速启动

```bash
# 1. 启动所有服务
docker-compose up -d

# 2. 启动 Ingestor
go run cmd/ingestor/main.go

# 3. 启动 Orchestrator
go run cmd/orchestrator/main.go

# 4. 测试上传
curl -X POST http://localhost:8080/api/v1/batches \
  -H "Content-Type: application/json" \
  -d '{"vehicle_id":"test-vehicle","vin":"TESTVIN123","expected_worker_count":3}'
```

### 5. 面试准备

**高频考点**（在 LEARNING_LOG.md 中）：
- Q1-Q11: Kafka 消息丢失、Exactly Once、Consumer Group
- Q12-Q17: DDD 聚合根设计、Repository 模式
- Q23-Q28: Redis Barrier、并发控制、状态机
- Q32-Q36: Go make 切片、Nil Pointer、并发 Save
- Q37-Q40: 补偿任务、DLQ 死信队列

### 6. 下一步工作

**本周重点**：Query Service 实现
- 报告查询 API
- Singleflight 防缓存击穿
- Redis 缓存集成

**下周重点**：AI Agent Worker
- LLM API 集成
- RAG 检索（可选）
- Token 优化

### 7. 重要提醒

⚠️ **开发前必读**：`LEARNING_LOG.md` 的"⚠️ 重要架构理解修正"部分

⚠️ **SQL 位置**：所有 SQL 查询必须在 Repository 层，不在 Service 层（DDD 分层）

⚠️ **补偿任务**：系统已有超时恢复机制（5 分钟 / 10 分钟）

---

**备注**：本项目由 Claude Code 辅助开发，每天更新 `LEARNING_LOG.md` 记录进度。
