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
