# Argus OTA Platform - Learning Log

---

## 📅 日期: 2025-01-18

### 1. 完成功能与技术选型

#### **功能 1: MinIO Client 实现流式上传**
- **实现**:
  - `internal/infrastructure/minio/client.go`
  - `PutObject()` 方法支持流式上传
  - 自动创建 Bucket（如果不存在）
- **为何这样设计**:
  - **流式上传**: 使用 `io.Reader` 接口，避免将整个文件加载到内存（防止 OOM）
  - **分片上传**: PartSize 设为 5MB，大文件自动分片（MinIO SDK 内部处理）
  - **为什么不零拷贝**: io.Copy 内部已优化（Linux splice 系统调用），性能足够

#### **功能 2: HTTP BatchHandler（Gin）**
- **实现**:
  - `POST /api/v1/batches` - 创建 Batch
  - `POST /api/v1/batches/:id/files` - 上传文件（流式）
  - `POST /api/v1/batches/:id/complete` - 完成上传（触发 Kafka）
- **为何这样设计**:
  - **两阶段上传**: 先上传文件到 MinIO，完成后再触发 Kafka 事件
  - **业务完整性**: 只有全部文件到齐才开始处理，避免部分数据的无效分析
  - **解耦上传与处理**: 上传阶段高并发低延迟，处理阶段 CPU 密集可分布式

#### **功能 3: Ingestor main.go（依赖注入 + 优雅关闭）**
- **实现**:
  - 依赖注入链：Config → Infrastructure → Repository → Service → Handler
  - 优雅关闭：监听 SIGINT/SIGTERM → HTTP Shutdown → DB Close → Kafka Close
- **为何这样设计**:
  - **依赖倒置**: Domain 定义接口，Infrastructure 实现接口
  - **关注点分离**: cmd 层只负责启动，业务逻辑在 internal
  - **优雅关闭**: 避免数据丢失，等待正在处理的请求完成

---

### 2. 面试高频考点

#### **Q1: 什么是零拷贝？如何实现？**
**A**:
- **传统拷贝**: 磁盘 → 内核态 → 用户态 → 内核态 → 网卡（4次拷贝）
- **零拷贝**: 磁盘 → 内核态 → 网卡（2次拷贝）
- **实现方式**:
  - Linux `sendfile`: `sendfile(socket, file, &offset, count)`
  - `splice`: `splice(pipefd[0], NULL, sockfd, NULL, len, 0)`
  - Go `io.Copy`: 自动使用 splice（如果系统支持）

#### **Q2: MinIO SDK 是零拷贝吗？**
**A**:
- **部分是**: MinIO SDK 使用 `io.Copy`，会自动优化
- **但不是完全零拷贝**: HTTP 层解析（multipart）需要拷贝
- **如果要完全零拷贝**: 使用 Presigned URL（客户端直传 MinIO）

#### **Q3: 如何优雅关闭 Go HTTP Server？**
**A**:
```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := server.Shutdown(ctx); err != nil {
    log.Error("Server shutdown error:", err)
}
```
- **常见错误**:
  1. 没有 context 超时 → 永久阻塞
  2. 没有等待请求完成 → 数据丢失
  3. 忘记关闭 DB/Kafka → 资源泄露

#### **Q4: 数据库连接池如何配置？**
**A**:
```go
db.SetMaxOpenConns(25)         // 最大打开连接数
db.SetMaxIdleConns(5)          // 最大空闲连接数
db.SetConnMaxLifetime(5 * time.Minute)  // 连接最大生命周期
```
- **为什么 Idle < Open**: 避免资源浪费，保持少量空闲连接
- **为什么需要 Lifetime**: 避免长时间连接导致的问题（如数据库重启）

#### **Q5: 为什么用 DDD 分层？**
**A**:
- **Domain 层**: 纯业务模型（不依赖技术）
- **Application 层**: 用例层（业务编排）
- **Infrastructure 层**: 技术实现（Postgres, Kafka, MinIO）
- **Interfaces 层**: HTTP / SSE（对外接口）
- **优势**:
  - 依赖倒置：Domain 不依赖外部技术
  - 可测试性：可以轻松 mock 依赖
  - 可维护性：职责清晰，修改局部不影响整体

---

### 3. 踩坑与解决 (Troubleshooting)

#### **Bug 1: batchID 类型不匹配**
- **现象**:
  ```go
  batchID := c.Param("id")  // 返回 string
  batchService.AddFile(ctx, batchID, fileID)  // 期望 uuid.UUID
  ```
- **原因**: Gin 的 `c.Param()` 返回 string，但 Domain 层使用 `uuid.UUID`
- **解决**:
  ```go
  batchIDStr := c.Param("id")
  batchID, err := uuid.Parse(batchIDStr)
  if err != nil {
      c.JSON(400, gin.H{"error": "invalid batch id"})
      return
  }
  ```

#### **Bug 2: c.Params vs c.Param**
- **现象**: `batchID := c.Params("id")` 编译错误
- **原因**: `c.Params` 返回 `Params` 类型，`c.Param` 返回 string
- **解决**: 使用 `c.Param("id")`（单数）

#### **Bug 3: Receiver 指针缺失**
- **现象**:
  ```go
  func (h batchHandler) CompleteUpload(c *gin.Context) { ... }
  ```
- **原因**: Method receiver 应该是指针（避免拷贝）
- **解决**:
  ```go
  func (h *batchHandler) CompleteUpload(c *gin.Context) { ... }
  ```

#### **Bug 4: MinIO 错误处理不完整**
- **现象**:
  ```go
  exists, err := client.BucketExists(ctx, bucket)
  if !exists { client.MakeBucket(...) }  // err 未检查
  ```
- **原因**: 只检查 `exists`，没有检查 `err`
- **解决**:
  ```go
  exists, err := client.BucketExists(ctx, bucket)
  if err != nil {
      return nil, fmt.Errorf("check bucket exists error: %w", err)
  }
  ```

#### **Bug 5: main.go 参数传递错误**
- **现象**:
  ```go
  server := startServer(router, cfg.Database.Host)  // 传了 Host
  ```
- **原因**: Copy-paste 错误，应该传 Port
- **解决**:
  ```go
  server := startServer(router, strconv.Itoa(cfg.Server.Port))
  ```

---

### 4. 下一步计划

#### **待完成的模块**
- ⬜ **Ingestor main.go 修复**:
  - [ ] 修复 line 81: `mustAtoi(getEnv("DB_PORT","5432"), "DB_PORT")`
  - [ ] 修复 line 114: `db.SetConnMaxIdleTime(5 * time.Minute)`
  - [ ] 修复 line 225: `startServer(router, strconv.Itoa(cfg.Server.Port))`
  - [ ] 添加 `db.SetMaxOpenConns(25)`

- ⬜ **PostgreSQL Migration**:
  - [ ] 创建 `batches` 表
  - [ ] 创建 `files` 表
  - [ ] 创建索引（VIN, status, created_at）

- ⬜ **Orchestrator Service**:
  - [ ] 实现 Kafka Consumer
  - [ ] 实现状态机（pending → uploaded → scattering → gathering → completed）
  - [ ] 实现 Redis Barrier（Scatter-Gather 计数）

- ⬜ **Docker Compose**:
  - [ ] PostgreSQL
  - [ ] MinIO
  - [ ] Kafka (Redpanda)
  - [ ] Redis

- ⬜ **测试**:
  - [ ] 单元测试（BatchService, BatchHandler）
  - [ ] 集成测试（上传文件 → 触发 Kafka）
  - [ ] 压力测试（并发上传）

---

### 5. 今日代码统计

- **新增文件**: 3
  - `internal/infrastructure/minio/client.go` (41 行)
  - `internal/interfaces/http/handlers/batch_handler.go` (120 行)
  - `cmd/ingestor/main.go` (230 行)

- **修改文件**: 1
  - `go.mod` (添加 Gin 和 MinIO SDK 依赖)

- **总代码量**: ~391 行

- **依赖添加**:
  - `github.com/gin-gonic/gin v1.11.0`
  - `github.com/minio/minio-go/v7 v7.0.98`

---

### 6. 关键设计决策总结

| 决策点 | 选择 | 原因 |
|--------|------|------|
| HTTP 框架 | Gin | 高性能、易用、社区活跃 |
| 文件上传 | 流式传输 | 避免大文件 OOM |
| 触发处理 | 两阶段上传 | 业务完整性（全文件到齐才处理） |
| 架构模式 | DDD | 依赖倒置、可测试、可维护 |
| 关闭策略 | 优雅关闭 | 避免数据丢失、资源泄露 |
| MinIO SDK | io.Copy | 自动零拷贝优化（splice） |

---

**备注**:
- 今天重点在**接入层**（Ingestor + HTTP Handler）
- 明天重点在**编排层**（Orchestrator + 状态机 + Kafka Consumer）
- 核心难点：**Scatter-Gather 分布式协调**（Redis Barrier）

---

## 📅 日期: 2025-01-19 (Day 6 - Infrastructure Day)

### 1. 完成功能与技术选型

#### **功能 1: PostgreSQL Schema 完善**
- **实现**:
  - `deployments/init-scripts/01-init-schema.sql`
  - 启用 `pgvector` 扩展（支持 RAG 向量搜索）
  - 添加关键 CHECK 约束（`processed_files <= total_files`）
  - 添加 `ai_diagnoses` 表的 `embedding` 字段（vector(1536)）
  - 添加向量索引（IVFFlat，支持相似度搜索）
  - 添加 `top_error_codes` JSONB 字段（灵活存储错误分析）
  - 添加 `batch_id UNIQUE` 约束（一个 Batch 只有一个诊断报告）
- **为何这样设计**:
  - **CHECK 约束**: 数据库层面保护业务规则（防止计数器超过 100%）
  - **pgvector**: 为未来 RAG 准备，支持语义搜索（pgvector 扩展 + 向量索引）
  - **JSONB vs 另建表**: 性能与灵活性的平衡（JSONB 支持 GIN 索引，查询能力强）
  - **UNIQUE 约束**: 保证幂等性（重复执行 AI 诊断会覆盖，而不是产生多条）

#### **功能 2: Docker Compose 修复**
- **实现**:
  - `deployments/docker-compose.yml`
  - PostgreSQL 镜像改为 `pgvector/pgvector:pg15`
- **为何这样设计**:
  - **pgvector 镜像**: 预装 pgvector 扩展，无需手动编译
  - **PG 15 版本**: 稳定版本，支持向量索引（IVFFlat）

#### **功能 3: Kafka 消息丢失应对方案（理论 + 实践）**
- **核心理论**: 三层防护机制
  1. **生产者侧**: `acks=-1` + `重试` + `幂等性`
  2. **Broker 侧**: `replication.factor=3` + `min.insync.replicas=2` + `刷盘策略`
  3. **消费者侧**: `手动提交 offset` + `死信队列`
- **为何这样设计**:
  - **acks=-1**: 等待所有 ISR 副本确认（最安全，性能 -30%）
  - **幂等性**: 防止重试导致重复（PID + Sequence Number 去重）
  - **手动提交**: 处理成功才提交 offset（不会丢数据，但可能重复消费）
  - **死信队列**: 避免消息一直重试导致阻塞（失败消息发送到 DLQ）

---

### 2. 面试高频考点

#### **Q6: PostgreSQL CHECK 约束的作用？**
**A**:
- **数据完整性**: 防止插入非法数据（如 `status = 'unknown'`）
- **业务规则保护**: 如 `processed_files <= total_files` 防止计数器超过 100%
- **早期错误发现**: 应用层 bug 会立即暴露（数据库拒绝写入）
- **文档作用**: 约束即文档，一目了然所有合法状态

#### **Q7: 为什么用 JSONB 而不是另建表？**
**A**:
| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| 另建表 | 规范化，易于查询 | JOIN 开销大，过度设计 | 结构化数据，字段固定 |
| JSONB | 灵活，支持部分索引 | 不如表规范 | 半结构化数据，字段动态 |
| TEXT | 简单 | 无法查询，无法索引 | 纯日志，不需要查询 |

**你的系统**: `top_error_codes` 用 JSONB（结构动态，需要查询）

#### **Q8: pgvector 如何实现相似度搜索？**
**A**:
```sql
-- 1. 创建向量索引（IVFFlat）
CREATE INDEX idx_diagnoses_embedding ON ai_diagnoses
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- 2. 相似度查询（余弦相似度）
SELECT diagnosis_summary, embedding <-> '[0.1, 0.2, ...]' AS distance
FROM ai_diagnoses
ORDER BY embedding <-> '[0.1, 0.2, ...]'
LIMIT 5;

-- 3. 距离算子
-- <-> : 欧氏距离（L2）
-- <=> : 余弦相似度（需要 vector_cosine_ops）
-- <#> : 负内积（negative dot product）
```

**面试考点**:
- **IVFFlat 索引**: 分层索引（100 个聚类），适合大规模数据
- **lists 参数**: 聚类数量（数据量大时增大，数据量小时减小）
- **为什么不用 HNSW**: IVFFlat 构建更快（适合频繁插入），HNSW 查询更快（适合静态数据）

#### **Q9: Kafka 如何保证消息不丢失？**（面试必考）
**A（标准答案，3 层防护）**:
1. **生产者侧**:
   - `acks=-1`（等待所有 ISR 副本确认）
   - `retries=5`（重试 5 次）
   - `enable.idempotence=true`（幂等性，防重复）
2. **Broker 侧**:
   - `replication.factor=3`（3 副本）
   - `min.insync.replicas=2`（最少 2 个副本写入成功）
   - `log.flush.interval.ms=1000`（每 1 秒刷盘）
   - `unclean.leader.election.enable=false`（不允许非 ISR 副本成为 Leader）
3. **消费者侧**:
   - `enable.auto.commit=false`（手动提交 offset）
   - 死信队列（DLQ，处理失败消息）

**权衡**: 数据安全 vs 性能
- 最安全配置：`acks=-1` + `手动提交` → **延迟 +50%，吞吐量 -30%**
- 高性能配置：`acks=1` + `自动提交` → **延迟 -50%，吞吐量 +30%**

**你的系统**: OTA 平台不能丢数据 → 用最安全配置

#### **Q10: Kafka 什么情况下会丢数据？**
**A（3 种场景）**:
1. **生产者**: `acks=0` + 网络抖动 → 消息未到达 Broker
2. **Broker**: `replication.factor=1` + Leader 宕机 → 数据未复制到 Follower
3. **消费者**: `自动提交` + 崩溃 → offset 已提交但消息未处理

**应对策略**:
- 生产者：`acks=-1` + `重试` + `幂等性`
- Broker：`replication.factor=3` + `min.insync.replicas=2`
- 消费者：`手动提交` + `死信队列`

#### **Q11: 如何实现 Exactly Once 语义？**
**A（3 个条件）**:
1. **生产者幂等**: `idempotence=true`（Kafka 0.11+ 支持）
2. **事务支持**: Kafka 0.11+ 支持跨分区事务（原子写入多个分区）
3. **消费者幂等**: 业务逻辑设计为幂等（如使用 `batch_id` 作为唯一键）

**示例**:
```go
// 消费者幂等：使用 batch_id 作为唯一键
INSERT INTO ai_diagnoses (batch_id, diagnosis_summary)
VALUES ($1, $2)
ON CONFLICT (batch_id) DO UPDATE SET diagnosis_summary = $2;
```

---

### 3. 踩坑与解决 (Troubleshooting)

#### **Bug 6: PostgreSQL 镜像不支持 pgvector**
- **现象**:
  ```bash
  ERROR: extension "vector" is not available
  ```
- **原因**: `postgres:15-alpine` 镜像没有预装 pgvector 扩展
- **解决**:
  ```yaml
  # docker-compose.yml
  image: pgvector/pgvector:pg15  # 使用 pgvector 官方镜像
  ```

#### **Bug 7: CHECK 约束太严格导致合法数据被拒绝**
- **现象**:
  ```sql
  ERROR: new row violates check constraint "processed_files_check"
  ```
- **原因**: 初始插入时 `total_files=0`，但约束是 `processed_files <= total_files`
- **解决**:
  ```sql
  -- 调整约束，允许 total_files=0 的情况
  ALTER TABLE batches
  DROP CONSTRAINT processed_files_check,
  ADD CONSTRAINT processed_files_check CHECK (
      (total_files = 0 AND processed_files = 0) OR
      (total_files > 0 AND processed_files >= 0 AND processed_files <= total_files)
  );
  ```

#### **Bug 8: 向量索引创建失败**
- **现象**:
  ```bash
  ERROR: index method "ivfflat" is not available
  ```
- **原因**: IVFFlat 索引需要至少 1000 行数据才能创建
- **解决**:
  ```sql
  -- 先插入数据，再创建索引
  CREATE INDEX CONCURRENTLY idx_diagnoses_embedding
  ON ai_diagnoses
  USING ivfflat (embedding vector_cosine_ops)
  WITH (lists = 100);
  ```

---

### 4. 下一步计划

#### **待完成的模块**
- ⬜ **Docker 验证**:
  - [ ] 启动所有 Docker 服务（`docker-compose up -d`）
  - [ ] 验证 PostgreSQL 连通（`psql -h localhost -U argus -d argus_ota`）
  - [ ] 验证 MinIO 连通（访问 http://localhost:9001）
  - [ ] 验证 Kafka 连通（`kafka-console-producer --broker-list localhost:9092 --topic test`）
  - [ ] 验证 Redis 连通（`redis-cli ping`）

- ⬜ **Ingestor 端到端测试**:
  - [ ] 启动 Ingestor（`go run cmd/ingestor/main.go`）
  - [ ] 创建 Batch（`curl -X POST http://localhost:8080/api/v1/batches ...`）
  - [ ] 上传文件（`curl -X POST http://localhost:8080/api/v1/batches/{id}/files ...`）
  - [ ] 完成上传（`curl -X POST http://localhost:8080/api/v1/batches/{id}/complete`）
  - [ ] 验证 PostgreSQL 数据（`SELECT * FROM batches WHERE id = '...';`）
  - [ ] 验证 Kafka 事件（`kafka-console-consumer --bootstrap-server localhost:9092 --topic batch-events --from-beginning`）

- ⬜ **Redis Client 封装**:
  - [ ] 实现 `internal/infrastructure/redis/client.go`
  - [ ] 实现 `INCR` 命令（分布式计数器）
  - [ ] 实现 `GET` / `DEL` 命令

- ⬜ **Orchestrator Service**:
  - [ ] 实现 Kafka Consumer（监听 `batch-events` topic）
  - [ ] 实现状态机驱动逻辑（pending → uploaded → scattering → gathering → completed）
  - [ ] 实现 Redis Barrier（Scatter-Gather 计数）

---

### 5. 今日代码统计

- **修改文件**: 2
  - `deployments/init-scripts/01-init-schema.sql` (+30 行)
    - 启用 pgvector 扩展
    - 添加 CHECK 约束（processed_files, completed_worker_count）
    - 添加 batch_id UNIQUE 约束
    - 添加 top_error_codes JSONB 字段
    - 添加 embedding vector(1536) 字段
    - 添加向量索引（IVFFlat）
  - `deployments/docker-compose.yml` (+1 行)
    - PostgreSQL 镜像改为 `pgvector/pgvector:pg15`

- **新增代码量**: ~31 行

- **理论输出**:
  - Kafka 消息丢失应对方案（3000+ 字）
  - 6 个面试高频考点（Q6-Q11）

---

### 6. 关键设计决策总结

| 决策点 | 选择 | 原因 |
|--------|------|------|
| PostgreSQL 镜像 | pgvector/pgvector:pg15 | 预装 pgvector 扩展，支持 RAG |
| CHECK 约束 | 添加 processed_files <= total_files | 数据库层面保护业务规则 |
| batch_id 约束 | UNIQUE | 一个 Batch 只有一个诊断报告（幂等性） |
| top_error_codes | JSONB | 灵活存储动态数据，支持查询 |
| embedding | vector(1536) | OpenAI Ada Embedding V2 维度 |
| 向量索引 | IVFFlat | 构建快，适合频繁插入 |
| Kafka acks | -1 (WaitForAll) | OTA 平台不能丢数据 |
| Kafka offset | 手动提交 | 处理成功才提交，不会丢数据 |
| Kafka 幂等性 | enable.idempotence=true | 防止重试导致重复 |

---

**备注**:
- 今天重点在**基础设施层**（PostgreSQL Schema + Docker Compose + Kafka 消息丢失方案）
- 明天重点在**编排层**（Orchestrator Service + Kafka Consumer + Redis Barrier）
- 核心难点：**Scatter-Gather 分布式协调**（Redis INCR 计数 + 状态机驱动）

---

## 📅 日期: 2025-01-19 (Day 6 - 实战测试与 Bug 修复)

### 1. 完成功能与技术选型

#### **功能 1: Docker 环境搭建（成功运行）**
- **实现**:
  - 启动所有 Docker 服务（PostgreSQL + Redis + Kafka + MinIO + Zookeeper）
  - 验证所有服务连通性（PING/查询/API 测试）
  - PostgreSQL 自动执行 init-scripts（4 个表创建成功）
- **为何这样设计**:
  - **PostgreSQL:15-alpine**: 镜像小（387MB），启动快，兼容性好
  - **暂缓 pgvector**: 先让系统跑起来，pgvector 可以后续手动编译
  - **Docker Compose**: 一键启动所有依赖，简化开发环境

#### **功能 2: 端到端测试（完整流程验证）**
- **实现**:
  - 创建 Batch API → PostgreSQL 记录创建
  - 上传文件 API → MinIO 存储成功
  - 完成上传 API → Batch 状态转换（pending → uploaded）
  - Kafka 事件发布 → BatchCreated 事件成功发送和消费
- **为何这样设计**:
  - **两阶段上传**: 先上传文件到 MinIO，再触发 Kafka 事件
  - **状态机驱动**: Batch 状态严格转换（pending → uploaded → scattering）
  - **异步解耦**: Kafka 事件驱动，Ingestor 不直接调用 Worker

#### **功能 3: Bug 9 修复（File 记录未创建）**
- **问题**:
  ```go
  // ❌ 原代码：只增加计数，没有创建 File 记录
  func (b *Batch) AddFile(fileID uuid.UUID) error {
      b.TotalFiles++
      return nil
  }
  ```
- **修复**:
  1. 创建 `PostgresFileRepository`（完整 CRUD 实现）
  2. 重构 `BatchService.AddFile()`（创建 File 实体 + 保存到 DB）
  3. 修改 `Handler.UploadFile()`（传入 filename, size, minioPath）
  4. 更新 Ingestor 依赖注入（添加 FileRepository）

- **为何这样设计**:
  - **聚合根原则**: Batch 是聚合根，应该管理 File 的创建
  - **职责分离**: Handler 只负责接收请求，Service 负责业务逻辑，Repository 负责持久化
  - **数据一致性**: File 记录和 Batch.total_files 在同一个事务中更新

#### **功能 4: Bug 10 修复（Batch.total_files 未更新）**
- **问题**:
  ```sql
  -- ❌ 原代码：ON CONFLICT 没有更新 total_files
  ON CONFLICT (id) DO UPDATE SET
      status = EXCLUDED.status,
      processed_files = EXCLUDED.processed_files,
      updated_at = EXCLUDED.updated_at
  ```
- **修复**:
  ```sql
  -- ✅ 修复后：添加 total_files 更新
  ON CONFLICT (id) DO UPDATE SET
      status = EXCLUDED.status,
      total_files = EXCLUDED.total_files,
      processed_files = EXCLUDED.processed_files,
      updated_at = EXCLUDED.updated_at
  ```
- **为何这样设计**:
  - **UPSERT 模式**: INSERT ... ON CONFLICT DO UPDATE 实现"插入或更新"
  - **完整更新**: 所有可变字段都需要在 UPDATE 子句中列出
  - **面试考点**: 为什么不用 REPLACE？（REPLACE 会删除再插入，丢失外键关系）

---

### 2. 面试高频考点

#### **Q12: File 为什么不用独立的聚合根？**（DDD 设计）
**A**:
- **聚合根定义**: 聚合根是唯一允许通过 Repository 访问的实体
- **File 的角色**: File 是 Batch 的子实体，不会独立存在
- **为什么这样设计**:
  - 业务逻辑：文件必须归属某个 Batch，没有独立查询场景
  - 数据一致性：通过 Batch 管理 File，保证 total_files 准确
  - 性能优化：减少 Repository 数量，简化依赖关系
- **何时需要独立聚合根**:
  - File 有复杂的生命周期（如独立的状态转换）
  - 需要独立查询（如"查询所有解析失败的文件"）
  - 多个聚合根共享（如 File 被多个 Batch 引用）

#### **Q13: 为什么 BatchRepository.Save() 用 UPSERT 而不是 INSERT + UPDATE？**
**A**:
- **UPSERT 优势**:
  - 原子操作：数据库层面保证一致性
  - 代码简洁：不需要判断是 INSERT 还是 UPDATE
  - 性能更好：一次数据库交互
- **INSERT + UPDATE 劣势**:
  - 需要先查询是否存在（额外一次数据库交互）
  - 并发问题：查询和插入之间可能有其他事务插入
  - 代码复杂：需要处理"已存在"和"不存在"两种情况
- **实现方式**:
  ```sql
  INSERT INTO batches (...) VALUES (...)
  ON CONFLICT (id) DO UPDATE SET ...
  ```

#### **Q14: PostgreSQL ON CONFLICT 的性能如何？**
**A**:
- **性能**:
  - 无冲突时：等同于 INSERT（没有额外开销）
  - 有冲突时：等同于 UPDATE（没有额外查询）
  - 索引查找：O(log n)，比先 SELECT 再 INSERT 快
- **适用场景**:
  - 幂等性操作（如重试）
  - 状态更新（如 Batch 状态转换）
  - 计数器更新（如 total_files++）
- **注意事项**:
  - 需要唯一索引或主键（这里是 id）
  - UPDATE 子句中必须列出所有字段（否则会用旧值覆盖）

#### **Q15: 如何保证 Batch 和 File 的事务一致性？**
**A**:
- **当前实现**:
  ```go
  // 1. 创建 File
  if err := s.fileRepo.Save(ctx, file); err != nil {
      return err
  }
  // 2. 更新 Batch
  if err := batch.AddFile(fileID); err != nil {
      return err
  }
  return s.batchRepo.Save(ctx, batch)
  ```
- **问题**: 如果第二步失败，File 记录已经创建，但 Batch.total_files 没有更新
- **解决方案**（未来优化）:
  1. **数据库事务**: 使用 BEGIN/COMMIT 包装两个操作
  2. **Saga 模式**: 补偿事务（如果 Batch 失败，删除 File）
  3. **最终一致性**: 定期扫描 total_files 与实际 file_count 不一致的 Batch
- **面试考点**: 分布式事务 vs 本地事务 vs 最终一致性

#### **Q16: MinIO 文件上传成功但数据库记录失败怎么办？**
**A**:
- **问题**: MinIO 上传成功 → 创建 File 记录失败 → 数据不一致
- **解决方案**:
  1. **重试机制**: 重新创建 File 记录（幂等性保证）
  2. **补偿事务**: 定期扫描 MinIO 中有文件但数据库无记录的情况
  3. **TCC 模式**: Try-Confirm-Cancel（先预留 MinIO 路径，确认后再上传）
- **当前选择**: 简化处理（手动修复），生产环境需要补偿事务

#### **Q17: 如何测试文件上传流程？**
**A**:
- **单元测试**:
  ```go
  // Mock MinIO Client 和 FileRepository
  mockMinIO.On("PutObject", ...).Return(nil)
  mockFileRepo.On("Save", ...).Return(nil)
  err := service.AddFile(ctx, batchID, fileID, "test.log", 100, "batch/file")
  assert.NoError(t, err)
  ```
- **集成测试**:
  ```bash
  # 使用真实 MinIO 和 PostgreSQL
  docker-compose up -d
  curl -F "file=@test.log" http://localhost:8080/api/v1/batches/{id}/files
  docker exec argus-postgres psql -c "SELECT * FROM files;"
  ```
- **端到端测试**: 完整流程验证（创建 → 上传 → 完成 → Kafka）

---

### 3. 踩坑与解决 (Troubleshooting)

#### **Bug 9: File 记录未创建**
- **现象**:
  ```bash
  # 上传文件成功
  curl -F "file=@test.log" http://localhost:8080/api/v1/batches/{id}/files
  # 返回成功：{"file_id": "...", "size": 47}

  # 但数据库中没有 File 记录
  docker exec argus-postgres psql -c "SELECT * FROM files;"  # 0 rows
  ```
- **原因**:
  1. `Batch.AddFile()` 只增加计数，没有创建 File 实体
  2. 没有 FileRepository 实现
  3. Handler 没有传入完整的文件信息（filename, size, minioPath）
- **解决**:
  ```go
  // 1. 创建 PostgresFileRepository
  func (r *PostgresFileRepository) Save(ctx context.Context, file *domain.File) error {
      query := `INSERT INTO files (...) VALUES (...)`
      _, err := r.db.ExecContext(ctx, query, file.ID, file.BatchID, ...)
      return err
  }

  // 2. 重构 BatchService.AddFile
  func (s *BatchService) AddFile(...) error {
      // 创建 File 实体
      file := &domain.File{
          ID: fileID,
          BatchID: batchID,
          OriginalFilename: originalFilename,
          FileSize: fileSize,
          MinIOPath: minioPath,
          ProcessingStatus: domain.FileStatusPending,
      }
      // 保存 File
      if err := s.fileRepo.Save(ctx, file); err != nil {
          return err
      }
      // 更新 Batch 计数
      batch.AddFile(fileID)
      return s.batchRepo.Save(ctx, batch)
  }
  ```

#### **Bug 10: Batch.total_files 未更新**
- **现象**:
  ```bash
  # 上传文件成功，File 记录已创建
  # 但 Batch.total_files 还是 0
  SELECT id, total_files FROM batches WHERE id = '...';
  # total_files = 0  # ❌ 应该是 1
  ```
- **原因**:
  ```sql
  -- ON CONFLICT 子句中没有更新 total_files
  ON CONFLICT (id) DO UPDATE SET
      status = EXCLUDED.status,
      -- 缺少 total_files
      processed_files = EXCLUDED.processed_files
  ```
- **解决**:
  ```sql
  -- 添加 total_files 更新
  ON CONFLICT (id) DO UPDATE SET
      status = EXCLUDED.status,
      total_files = EXCLUDED.total_files,  -- ✅ 添加这一行
      processed_files = EXCLUDED.processed_files
  ```

#### **Bug 11: Kafka 容器启动失败**
- **现象**:
  ```
  ERROR [KafkaServer id=1] Exiting Kafka due to fatal exception
  org.apache.zookeeper.KeeperException$NodeExistsException
  ```
- **原因**: ZooKeeper 中有 Kafka 的旧数据（broker.id 冲突）
- **解决**:
  ```bash
  # 清理所有容器和 volumes
  docker compose down -v
  # 重新启动
  docker compose up -d
  ```

#### **Bug 12: pgvector 镜像拉取失败**
- **现象**:
  ```
  Error: failed to resolve reference "docker.io/pgvector/pgvector:pg15": EOF
  ```
- **原因**: 网络问题，Docker Hub 连接超时
- **解决**:
  ```yaml
  # 暂时使用 postgres:15-alpine
  # 注释掉 pgvector 扩展
  -- CREATE EXTENSION IF NOT EXISTS "vector";
  ```

---

### 4. 下一步计划

#### **待完成的模块**
- ⬜ **Redis Client 封装**:
  - [ ] 实现 `internal/infrastructure/redis/client.go`
  - [ ] 实现 `INCR` 命令（分布式计数器）
  - [ ] 实现 `GET` / `DEL` 命令

- ⬜ **Orchestrator Service**:
  - [ ] 实现 Kafka Consumer（监听 `batch-events` topic）
  - [ ] 实现状态机驱动逻辑（pending → uploaded → scattering → gathering → completed）
  - [ ] 实现 Redis Barrier（Scatter-Gather 计数）

- ⬜ **Mock Worker**:
  - [ ] 实现 Go 版本的 C++ Worker（模拟解析）
  - [ ] 实现 Go 版本的 Python Worker（模拟聚合）

---

### 5. 今日代码统计

- **修改文件**: 5
  - `internal/infrastructure/postgres/repository.go` (+120 行)
    - 创建 PostgresFileRepository（完整 CRUD 实现）
  - `internal/application/batch_service.go` (+40 行)
    - 重构 AddFile 方法（创建 File 实体）
  - `internal/interfaces/http/handlers/batch_handler.go` (+10 行)
    - 更新 AddFile 调用（传入完整参数）
  - `cmd/ingestor/main.go` (+2 行)
    - 添加 FileRepository 依赖注入
  - `deployments/init-scripts/01-init-schema.sql` (+30 行)
    - PostgreSQL Schema 完善（添加 pgvector 支持，已注释）
  - `deployments/docker-compose.yml` (+1 行)
    - 镜像改为 postgres:15-alpine

- **新增代码量**: ~203 行

- **Bug 修复**: 2 个（Bug 9 + Bug 10）

---

### 6. 关键设计决策总结

| 决策点 | 选择 | 原因 |
|--------|------|------|
| PostgreSQL 镜像 | postgres:15-alpine | 镜像小、启动快、兼容性好 |
| pgvector 扩展 | 暂缓 | 先让系统跑起来，后续手动编译 |
| File 聚合根 | 否（子实体） | File 必须归属 Batch，不会独立存在 |
| FileRepository | 独立实现 | 虽然是子实体，但需要独立的 CRUD 操作 |
| AddFile 重构 | 创建 File + 更新 Batch | 保证数据一致性（File 记录 + 计数） |
| UPSERT 模式 | ON CONFLICT DO UPDATE | 原子操作、代码简洁、性能好 |
| 事务一致性 | 暂时简化 | 当前实现无事务，未来用 Saga 模式 |

---

**备注**:
- 今天重点在**实战测试**（Docker 验证 + 端到端测试 + Bug 修复）
- **关键突破**: File 记录创建问题解决，系统真正跑起来了！
- 明天重点在**编排层**（Orchestrator Service + Kafka Consumer + Redis Barrier）
- 核心难点：**Scatter-Gather 分布式协调**（Redis INCR 计数 + 状态机驱动）

---

### 📅 日期: 2026-01-21 (Day 7)

#### 1. 完成功能与技术选型

**功能 1：Redis Client 完整实现**
- **为何这样设计**：
  - 使用 `go-redis/v9` 官方 SDK（性能好、维护活跃）
  - 连接池配置（PoolSize=10, MinIdleConns=5）避免资源耗尽
  - 超时配置（DialTimeout=5s, ReadTimeout=3s）防止永久阻塞
  - `redis.Nil` 特殊处理（key 不存在不是错误）
- **高并发/海量数据优势**：
  - Redis QPS ~100000（比 PostgreSQL 快 100 倍）
  - 单线程模型（无锁竞争）
  - 内存操作（微秒级延迟）

**功能 2：Kafka Consumer 完整实现**
- **为何这样设计**：
  - 使用 Consumer Group（支持水平扩展、故障转移）
  - 手动提交 offset（可靠性保证，不丢数据）
  - 无限循环消费（Rebalance 自动恢复）
  - 回调函数模式（解耦 Kafka 层和业务逻辑）
- **高并发/海量数据优势**：
  - 多个 Consumer 实例负载均衡（自动分配 partition）
  - 一个实例崩溃，其他实例接管（高可用）
  - offset 管理（自动或手动提交）

**功能 3：Orchestrator 完整实现**
- **为何这样设计**：
  - 4 层架构（Messaging → Infrastructure → Application → cmd）
  - 事件驱动（Kafka 发布-订阅模式）
  - Redis Barrier（Set 集合，天然幂等）
  - 状态机驱动（pending → uploaded → scattering）
- **DDD 概念对应**：
  - Messaging 层：接口定义（依赖倒置）
  - Infrastructure 层：技术实现（Kafka Consumer）
  - Application 层：业务编排（状态机、Barrier 检查）
  - Domain 层：业务规则（Batch.TransitionTo）

**功能 4：Redis Barrier 实现**
- **为何这样设计**：
  - 使用 Set 集合（SADD + SCARD）而非 INCR 计数器
  - 天然幂等（重复添加同一 fileID，集合大小不变）
  - 不需要额外的去重逻辑
  - 抗故障（重试安全）
- **高并发/海量数据优势**：
  - 原子操作（SADD 是原子操作）
  - 无锁竞争（Redis 单线程模型）
  - 性能高（比 PostgreSQL 行锁快 100 倍）

#### 2. 面试高频考点

**Q23: Kafka Consumer 的 Offset 配置有什么讲究？**（⭐⭐⭐⭐⭐）
**A**:
```
OffsetNewest vs OffsetOldest：

1. OffsetNewest（只消费新消息）
   - 优点：启动快（不处理历史消息）
   - 缺点：可能丢失数据（旧消息不处理）
   - 适用：实时性要求高，可容忍数据丢失

2. OffsetOldest（从最早的消息开始）✅ 推荐
   - 优点：不丢数据（所有消息都处理）
   - 缺点：启动慢（需要处理历史消息）
   - 适用：不能丢数据（如 OTA 平台）

OTA 平台选择：
→ 用 OffsetOldest（不能丢数据）
```

**Q24: 为什么 Orchestrator 需要 Kafka Producer？**（⭐⭐⭐⭐）
**A**:
```
事件驱动架构的核心：

1. 消费事件
   Orchestrator ← Kafka ← Ingestor
   (BatchCreated 事件)

2. 处理业务逻辑
   - 状态转换（pending → uploaded → scattering）
   - Redis Barrier 检查

3. 发布新事件
   Orchestrator → Kafka → Workers
   (StatusChanged 事件)

4. 保持事件链完整
   BatchCreated → StatusChanged → FileParsed → AllFilesScattered
   
→ 没有 Producer，事件链就会断裂
```

**Q25: Redis Set 如何实现分布式 Barrier？**（⭐⭐⭐⭐⭐）
**A**:
```go
// 完整实现
func (s *OrchestrateService) handleFileParsed(event) error {
    // 1. SADD 记录已处理的文件（天然幂等）
    redis.SADD("batch:{id}:processed_files", fileID)
    
    // 2. SCARD 获取已处理文件数量
    count := redis.SCARD("batch:{id}:processed_files")
    
    // 3. 检查 Barrier
    if count == totalFiles {
        // ✅ 所有文件处理完成，触发 Gather
    }
}

// 优势分析：
// 1. 天然幂等（重复添加同一 fileID，集合大小不变）
// 2. 不需要额外的去重逻辑
// 3. 抗故障（重试安全）
// 4. 原子操作（SADD 是原子操作）
```

对比 INCR 方案：
```go
// ❌ INCR 方案（需要额外去重）
redis.SET("batch:{id}:file:{fileID}", "1", "NX")
if success {
    redis.INCR("batch:{id}:counter")
}
count := redis.GET("batch:{id}:counter")

// 问题：
// - 需要 2 次 Redis 调用（SETNX + INCR）
// - 需要额外的去重逻辑
// - 重试可能导致计数错误
```

**Q26: 为什么 Subscribe 里用无限循环？**（⭐⭐⭐⭐）
**A**:
```
Kafka Consumer 的消费模式：

1. Consume() 是阻塞调用
   - 消费一批消息后返回
   - Rebalance 时也会返回

2. 为什么需要无限循环？
   for {
       err := consumer.Consume(ctx, topics, handler)
       // Rebalance 后需要重新调用 Consume()
   }

3. 什么时候退出？
   - ctx.Done() 被取消（优雅关闭）
   - 收到系统信号（SIGINT/SIGTERM）

→ 无限循环是为了保证持续消费
```

**Q27: Kafka Consumer Group 的作用？**（⭐⭐⭐⭐⭐）
**A**:
```
Consumer Group 的核心功能：

1. 负载均衡
   - 多个 Consumer 实例自动分配 partition
   - 例如：3 个 partition，3 个 Consumer，每个 Consumer 处理 1 个 partition

2. 故障转移
   - 一个 Consumer 崩溃，其他 Consumer 接管
   - 自动触发 Rebalance（重新分配 partition）

3. offset 管理
   - 自动提交 offset（enable.auto.commit=true）
   - 手动提交 offset（enable.auto.commit=false）✅ 推荐
   - offset 存储在 Kafka 特殊 topic（__consumer_offsets）

4. 水平扩展
   - 增加 Consumer 实例 → 提高吞吐量
   - 减少 Consumer 实例 → 降低成本

→ Consumer Group 是 Kafka 高可用的核心
```

**Q28: 为什么手动提交 offset？**（⭐⭐⭐⭐⭐）
**A**:
```
自动提交 vs 手动提交：

1. 自动提交（enable.auto.commit=true）
   ❌ 问题：消息处理失败，但 offset 已提交
   - Consumer 收到消息
   - Kafka 自动提交 offset
   - Consumer 处理消息失败
   - → 消息丢失（无法重新消费）

2. 手动提交（enable.auto.commit=false）✅ 推荐
   ✅ 优势：只有处理成功才提交 offset
   - Consumer 收到消息
   - Consumer 处理消息
   - 处理成功 → 手动提交 offset
   - 处理失败 → 不提交 offset（可重新消费）
   
实现：
session.MarkMessage(msg, "")  // 手动标记消息
// Kafka 在适当时机提交 offset

→ OTA 平台用手动提交（可靠性优先）
```

#### 3. 踩坑与解决

**Bug 13：状态转换重复代码**
- **现象**：`handleBatchCreated` 中状态转换代码重复 5 次
- **原因**：复制粘贴错误
- **解决**：删除重复代码，只保留一次
- **经验**：写代码时要注意检查重复逻辑

**Bug 14：OffsetNewest 导致数据丢失**
- **现象**：Kafka 消息没有被消费
- **原因**：`OffsetNewest` 只消费新消息，旧消息丢失
- **解决**：改为 `OffsetOldest`（从最早的消息开始）
- **经验**：数据敏感系统必须用 `OffsetOldest`

**Bug 15：NewBalanceStrategyRoundRobin() 语法错误**
- **现象**：编译失败
- **原因**：`NewBalanceStrategyRoundRobin()` 是函数调用，应该用变量
- **解决**：改为 `BalanceStrategyRoundRobin`
- **经验**：注意 Sarama API 的使用方式

**Bug 16：缺少 main.go 初始化函数**
- **现象**：无法编译运行
- **原因**：只有 main 函数骨架，缺少所有初始化函数
- **解决**：补充完整实现（initDB, initRedis, initKafkaProducer）
- **经验**：main 函数需要完整的依赖注入链

#### 4. 下一步计划

**Day 8 任务**：
- [ ] Mock Worker 实现（模拟 C++ Worker 解析 rec 文件）
- [ ] 完整流程测试（Ingestor → Orchestrator → Workers → Redis Barrier）
- [ ] 验证状态转换：scattering → scattered → gathering → gathered

---

**备注**：
- 今天完成 **Orchestrator 完整实现**（Kafka Consumer + 状态机 + Redis Barrier）
- **关键突破**：Orchestrator 成功消费 Kafka 事件，状态转换成功！
- **系统完整度**：40%（核心流程已打通，还差 Worker 和 Query Service）

