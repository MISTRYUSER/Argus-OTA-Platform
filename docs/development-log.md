# Argus OTA Platform - 开发日志

## 2026-01-12 (Day 1)

### 完成事项
- ✅ 配置 MinIO 服务到 docker-compose.yml
- ✅ 创建环境变量配置 (deployments/env/.env.example)
- ✅ 成功启动 MinIO 服务
- ✅ 验证 MinIO 功能（上传/下载/列表）
- ✅ 设计并实现 `status.go` 基础框架
- ✅ 修复 status.go 的 4 个问题：
  1. ✅ 添加 StatusFailed 常量
  2. ✅ 添加失败状态转换路径：
     - scattering → scattered | failed
     - gathering → gathered | failed
     - diagnosing → completed | failed
  3. ✅ 添加 completed → pending 复用转换
  4. ✅ 删除空的 import

### status.go 最终状态转换图
```
pending → uploaded → scattering → scattered → gathering → gathered → diagnosing → completed
                           ↓              ↓              ↓
                         failed         failed         failed
                                                                ↓
                                                        completed → pending (复用)
```

### 技术决策
- 使用 `type BatchStatus string` 自定义类型（类型安全）
- 使用 `map[BatchStatus][]BatchStatus` 实现状态转换规则
- File 设计为独立聚合根（有独立 Repository）

### 已创建/修改的文件
- `deployments/docker-compose.yml` - 添加 MinIO 服务
- `deployments/env/.env.example` - MinIO 配置
- `internal/domain/status.go` - BatchStatus 实现

### 下一步计划
- 实现 `file.go` 领域模型
- 实现 `batch.go` 聚合根
- 添加领域事件 (events.go)
- 定义 Repository 接口 (repository.go)

### 服务信息
- MinIO Console: http://localhost:9001 (minioadmin/minioadmin)
- MinIO API: http://localhost:9000
- Bucket: argus-logs

### 参考资料
- 数据库 Schema: `deployments/init-scripts/01-init-schema.sql`
- 架构文档: `docs/Argus_OTA_Platform.md`

---

## 2026-01-15 (Day 2)

### 完成事项
- ✅ 实现 `file.go` 领域模型完整版
  - ✅ 补全 ProcessingStatus 状态定义（pending → parsing → parsed → aggregating → completed）
  - ✅ 实现 ProcessingStatus 状态机（CanTransitionTo 方法）
  - ✅ 状态转换与数据库 Schema 完全一致
  - ✅ 添加面试注释（为什么每个中间状态都允许 Failed）

- ✅ 实现 `events.go` 领域事件
  - ✅ 实现 BatchCreated 的 DomainEvent 接口
  - ✅ 实现 StatusChanged 的 DomainEvent 接口
  - ✅ 提供事件溯源基础设施

- ✅ 实现 `batch.go` 聚合根（部分）
  - ✅ NewBatch 构造函数
  - ✅ 参数校验（vehicleID, VIN, expectedWorkers）
  - ✅ BatchCreated 事件记录

### file.go 状态转换图
```
pending → parsing → parsed → aggregating → completed
           ↓          ↓           ↓
         failed    failed       failed
```

### 技术决策与面试重点
1. **状态机模式复用**
   - ProcessingStatus 与 BatchStatus 保持一致的设计模式
   - 使用 `map[ProcessingStatus][]ProcessingStatus` 实现状态转换规则

2. **事件驱动设计**
   - BatchCreated 和 StatusChanged 实现 DomainEvent 接口
   - 为后续 Kafka 事件发布奠定基础

3. **为什么 File 不支持 completed → pending？**
   - Batch 可以复用（重新上传文件）
   - File 处理是单向的（重新处理应创建新 File）

4. **为什么每个中间状态都允许 Failed？**
   - 任何一个步骤都可能失败（C++ 崩溃、数据异常、网络错误）

### 已创建/修改的文件
- `internal/domain/file.go` - File 聚合根 + ProcessingStatus 状态机
- `internal/domain/events.go` - 领域事件 + DomainEvent 接口实现
- `internal/domain/batch.go` - Batch 聚合根（NewBatch 已实现）

### 下一步计划
- ✅ 完成 batch.go 的 TransitionTo 状态转换方法
- ✅ 实现 Barrier 协调（IncrementWorkerCount）
- ✅ 实现文件进度跟踪（AddFile/MarkFileProcessed）
- ✅ 实现事件管理（GetEvents/ClearEvents）

---

## 2026-01-16 (Day 3)

### 完成事项
- ✅ **完整实现 `batch.go` 聚合根的所有方法**
  - ✅ `TransitionTo` - 状态转换 + 业务规则校验（调用 `BatchStatus.CanTransitionTo()`）
  - ✅ `IncrementWorkerCount` - Barrier 协调核心逻辑（检查 `CompletedWorkerCount < ExpectedWorkerCount`）
  - ✅ `AddFile` - 文件上传阶段跟踪（限制只能在 pending/uploaded 状态添加）
  - ✅ `MakeFileProcessed` - 文件处理进度跟踪（检查 `ProcessedFiles < TotalFiles`）
  - ✅ `GetEvents` - 事件查询（返回副本，保证封装性）
  - ✅ `ClearEvents` - 事件清空（Kafka 发布后调用）

- ✅ **定义 Repository 接口** (`internal/domain/repository.go`)
  - ✅ `BatchRepository` 接口 - 定义了 6 个核心方法
  - ✅ `FileRepository` 接口 - 定义了 4 个核心方法
  - ✅ 接口参数使用 `context.Context`（支持超时和链路追踪）
  - ✅ 返回值使用 `*Batch` 而非 `Batch`（聚合根需要可修改）

- ✅ **实现 PostgreSQL Repository** (`internal/infrastructure/postgres/repository.go`)
  - ✅ `PostgresBatchRepository` 实现 `domain.BatchRepository` 接口
  - ✅ `Save` - 使用 `INSERT ... ON CONFLICT DO UPDATE` 实现幂等性
  - ✅ `FindByID` - Scan 到 string 再转换为 `BatchStatus` 类型
  - ✅ `FindByStatus` - 查询特定状态的所有 Batch（用于任务调度）
  - ✅ `Delete` - 删除 Batch 并检查影响行数
  - ✅ 修复了 3 个关键 bug：
    1. `batch.Status.String()` 转换（Save 方法）
    2. `&statusStr` Scan 变量（FindByID/FindByStatus）
    3. `DELETE FROM` SQL 语法修复

### 核心理解：DDD 聚合根的设计原则

**关键领悟**：所有状态变化必须通过聚合根方法
- ✅ 外部不能直接修改 Batch 的字段（因为字段是导出的，但遵循约定）
- ✅ 状态转换规则封装在聚合根内（通过 `TransitionTo` 方法）
- ✅ 事件记录与状态变化原子性（每次状态变化都记录到 `eventlog`）
- ✅ 保证业务不变式始终成立（通过方法内的参数校验）

**架构分层清晰**：
```
Domain 层 (domain/)
  - 定义接口：BatchRepository
  - 定义聚合根：Batch, File
  - 定义状态机：BatchStatus, ProcessingStatus
  - 定义事件：BatchCreated, StatusChanged

Infrastructure 层 (infrastructure/postgres/)
  - 实现接口：PostgresBatchRepository 实现 domain.BatchRepository
  - 依赖数据库：*sql.DB
  - SQL 操作：INSERT/UPDATE/SELECT/DELETE

Application 层 (application/) - 下一步
  - 使用接口：依赖 domain.BatchRepository（不依赖具体实现）
  - 编排业务：调用 Batch 方法 → 保存到 Repository → 发布 Kafka 事件
```

### 技术决策与面试重点

1. **Repository 模式的价值**
   - **依赖倒置**：Domain 层定义接口，Infrastructure 层实现
   - **可测试性**：可以注入 Mock Repository 进行单元测试
   - **可替换性**：PostgreSQL → MySQL 只需改实现，Domain 层不变

2. **为什么 Save 用 ON CONFLICT 而非先 EXISTS？**
   - **原子性**：一次数据库操作，避免竞态条件
   - **性能**：两次操作（EXISTS + INSERT）vs 一次操作（UPSERT）
   - **幂等性**：多次调用 Save 不会导致重复数据

3. **为什么 FindByID 找不到返回 (nil, nil) 而非 error？**
   - **语义区分**："不存在"不是"错误"
   - **调用友好**：`if batch == nil { ... }` 比 `if err != nil && err.Error() == "not found" { ... }` 更清晰
   - **业界惯例**：Go 社区的常见实践

4. **为什么 Scan 到 string 再转换为 BatchStatus？**
   - **数据库存储**：PostgreSQL 的 VARCHAR 列是 string 类型
   - **类型安全**：Go 层使用 `BatchStatus` 自定义类型（避免魔法字符串）
   - **转换成本**：一次 string 转换的 CPU 开销可以接受

5. **为什么 GetEvents 返回副本？**
   - **封装性**：防止外部直接修改 `eventlog`，破坏数据一致性
   - **防御性编程**：`copy(events, b.eventlog)` 确保内部状态不被意外修改

### 代码修复经验

**Bug 1：类型转换问题**
```go
// ❌ 错误：batch.Status 是 BatchStatus，不是 string
batch.ID, batch.VIN, batch.Status, ...

// ✅ 正确：调用 String() 方法
batch.ID, batch.VIN, batch.Status.String(), ...
```

**Bug 2：Scan 目标变量类型**
```go
// ❌ 错误：不能 Scan 到自定义类型
var batch domain.Batch
err := db.QueryRow(...).Scan(&batch.Status)

// ✅ 正确：Scan 到 string 再转换
var statusStr string
err := db.QueryRow(...).Scan(&statusStr)
batch.Status = domain.BatchStatus(statusStr)
```

**Bug 3：SQL 语法错误**
```go
// ❌ 错误：DELETE 不需要 *
DELETE * FROM batches

// ✅ 正确：
DELETE FROM batches
```

### 已创建/修改的文件
- `internal/domain/batch.go` - 完整实现 6 个方法
- `internal/domain/repository.go` - 定义 BatchRepository 和 FileRepository 接口
- `internal/infrastructure/postgres/repository.go` - PostgreSQL 实现（4 个方法）

### 待优化点（留作后续改进）
- [ ] `TransitionTo` 缺少 completed → pending 复用逻辑（清空 ProcessedFiles/ErrorMessage/CompletedAt）
- [ ] `TransitionTo` 缺少 `StatusChanged` 事件记录
- [ ] `TransitionTo` 缺少 `CompletedAt` 设置（failed/completed 状态）
- [ ] `IncrementWorkerCount` 缺少自动触发 scattered → gathering 转换
- [ ] `FindByID` 的类型转换应该移到错误检查之后
- [ ] 缺少 `FindByVIN` 和 `List` 方法的实现

### 下一步计划
- [ ] 实现 Application 层 Service（BatchService）
- [ ] 集成 Kafka 事件发布
- [ ] 实现 Orchestrator（状态机编排 + Worker 调度）
- [ ] 实现 Redis Barrier（分布式计数器）
- [ ] 单元测试和集成测试

---

## 2026-01-18 (Day 4)

### 完成事项

#### 1. ✅ 实现 Application 层 BatchService (`internal/application/batch_service.go`)
- ✅ **CreateBatch** - 创建 Batch + 保存到 PostgreSQL + 发布 Kafka 事件
- ✅ **TransitionBatchStatus** - 状态转换 + 保存 + 发布 StatusChanged 事件
- ✅ **AddFile** - 添加文件到 Batch（检查状态：只能在 pending/uploaded 状态添加）
- ✅ **依赖倒置设计**：依赖 `messaging.KafkaEventPublisher` 接口，不依赖具体实现
- ✅ **事件发布流程**：调用 Domain 方法 → 保存到 Repository → 发布 Kafka 事件 → 清空事件日志

#### 2. ✅ 实现 Kafka 事件发布器
- ✅ **接口定义** (`internal/messaging/kafka_publisher.go`)
  - 定义 `KafkaEventPublisher` 接口（PublishEvents + Close）
  - 遵循依赖倒置原则：Domain/Application 层定义接口

- ✅ **Kafka 实现** (`internal/infrastructure/kafka/producer.go`)
  - 使用 `IBM/sarama` 库实现 SyncProducer
  - `PublishEvents` - 批量发布领域事件
  - `publishBatchCreated` - 发布 BatchCreated 事件（JSON 格式）
  - `publishStatusChanged` - 发布 StatusChanged 事件（包含 old_status 和 new_status）
  - **关键修复**：
    - 事件类型从 `domain.BatchStatusChanged` 改为 `domain.StatusChanged`
    - 添加 `.String()` 调用：`event.OldStatus.String()` / `event.NewStatus.String()`
    - 返回接口类型：`messaging.KafkaEventPublisher` 而非具体实现

#### 3. ✅ 创建 Kafka 集成测试 (`cmd/test-kafka/main.go`)
- ✅ 完整的端到端测试流程：
  1. 连接 PostgreSQL
  2. 创建 Kafka Producer
  3. 创建 BatchService（注入 Repository + Kafka）
  4. 测试 CreateBatch（触发 BatchCreated 事件）
  5. 测试 AddFile（在 pending 状态添加文件）
  6. 测试 TransitionBatchStatus（pending → uploaded → scattering）
  7. 查询 Batch 验证状态
- ✅ 修复 PostgreSQL 驱动缺失：添加 `_ "github.com/lib/pq"` 导入
- ✅ **测试成功运行**，输出日志显示 Kafka 事件成功发布：
  ```
  [Kafka] Producer created successfully. Brokers: [localhost:9092], Topic: batch-events
  [Kafka] Publishing 1 events to topic: batch-events
  [Kafka] BatchCreated sent successfully. Partition: 0, Offset: 0
  ✅ Batch created: ID=xxx, Status=pending
  ```

#### 4. ✅ 实现 BatchService 单元测试 (`internal/application/test/batch_service_test.go`)
- ✅ 创建 Mock 对象：
  - `MockBatchRepository` - Mock 所有 Repository 方法
  - `MockKafkaEventPublisher` - Mock Kafka 发布器
- ✅ **6 个测试用例全部通过**：
  1. `TestCreateBatch_Success` - 测试成功创建 Batch（验证 Save 被调用 2 次 + PublishEvents 1 次）
  2. `TestCreateBatch_RepositoryError` - 测试 Repository 保存失败（验证错误传播）
  3. `TestTransitionBatchStatus_Success` - 测试成功转换状态
  4. `TestTransitionBatchStatus_BatchNotFound` - 测试 Batch 不存在的错误处理
  5. `TestAddFile_Success` - 测试成功添加文件
  6. `TestAddFile_WrongStatus` - 测试在错误状态下添加文件（scattering 状态不允许添加）
- ✅ **测试修复记录**：
  - 包名从 `application` 改为 `application_test`
  - 添加 `internal/application` 导入
  - 修复 Mock 构造函数调用（移除重复参数）
  - 修复 `TestAddFile_WrongStatus` 的状态转换验证

#### 5. ✅ 架构理解修正：两阶段上传设计
- ✅ **关键修正**：Kafka 事件的触发时机
  - ❌ **错误理解**：上传文件时立即触发 Kafka 事件
  - ✅ **正确理解**：所有文件上传完成后才触发 Kafka 事件

- ✅ **两阶段上传架构**：

  **阶段 1：文件上传阶段（无 Kafka 事件）**
  ```
  车辆启动 → 上传 rec 文件 → 流式传输到 MinIO
             ↓
       Ingestor 记录 file_id（Batch.TotalFiles++）
             ↓
       等待所有文件上传完成...
             ↓
       车辆发送 /complete 信号
  ```

  **阶段 2：处理阶段（Kafka 驱动）**
  ```
  Ingestor 收到 /complete → 发布 BatchCreated 事件
                            ↓
                     Orchestrator 消费事件
                            ↓
                     状态机：pending → uploaded → scattering
                            ↓
                     调度 C++ Workers 处理文件
  ```

- ✅ **为什么这样设计？**
  1. **业务完整性**：只有全部文件到齐才开始处理（rec 文件是完整会话记录）
  2. **性能优化**：分离瓶颈资源（上传 vs 处理）
  3. **错误处理**：上传失败只重传单个文件，处理失败通过 Kafka 补偿
  4. **系统解耦**：Ingestor、Orchestrator、Workers 各司其职

#### 6. ✅ 更新架构文档 (`docs/Argus_OTA_Platform.md`)
- ✅ 更新"写入路径"章节，添加详细的 Mermaid 时序图
- ✅ 添加"两阶段上传设计详解"章节
- ✅ 补充设计决策说明（业务完整性、性能优化、错误处理、系统解耦）

### 核心理解：DDD + 事件驱动架构

**1. Application 层的职责**
```
Application 层 (application/batch_service.go)
  - 编排业务流程
  - 调用 Domain 层方法（batch.TransitionTo）
  - 调用 Infrastructure 层（repository.Save）
  - 发布领域事件（kafka.PublishEvents）
  - 不包含业务逻辑（业务逻辑在 Domain 层）
```

**2. 依赖倒置原则的实际应用**
```
Domain 层 (domain/)
  - 定义接口：BatchRepository
  - 定义聚合根：Batch
  - 不依赖任何技术实现

Messaging 层 (messaging/)
  - 定义接口：KafkaEventPublisher
  - 接口由 Application 层使用

Infrastructure 层 (infrastructure/)
  - 实现接口：PostgresBatchRepository implements domain.BatchRepository
  - 实现接口：KafkaEventProducer implements messaging.KafkaEventPublisher
  - 可以被替换（PostgreSQL → MySQL，Kafka → RabbitMQ）
```

**3. 事件发布流程**
```go
// 1. 调用 Domain 方法（状态变化 + 事件记录）
batch.TransitionTo(domain.BatchStatusUploaded)

// 2. 保存到 Repository（持久化状态）
s.batchRepo.Save(ctx, batch)

// 3. 发布 Kafka 事件（通知其他服务）
events := batch.GetEvents()
s.kafka.PublishEvents(ctx, events)

// 4. 清空事件日志（避免重复发布）
batch.ClearEvents()
```

### 技术决策与面试重点

**1. 为什么用 Kafka 而不是 HTTP 调用 Worker？**
   - **解耦**：Ingestor 不需要知道 Worker 的地址和数量
   - **异步**：Ingestor 立即返回，不阻塞上传流程
   - **可扩展**：Worker 可以动态增减，无需修改 Ingestor 代码
   - **重试机制**：Kafka 支持消息重试，HTTP 调用失败需要自己实现

**2. 为什么 Upload 完成后才触发 Kafka？**
   - **业务完整性**：rec 文件是完整会话记录，缺一不可
   - **避免无效处理**：部分文件的情况下，不应该开始分析
   - **性能优化**：上传阶段（网络瓶颈）vs 处理阶段（CPU 瓶颈）

**3. 为什么 Kafka Producer 返回接口而非具体实现？**
   - **依赖倒置**：Application 层依赖接口，不依赖具体实现
   - **可测试性**：可以注入 Mock Kafka 进行单元测试
   - **可替换性**：Kafka → RabbitMQ 只需修改 Infrastructure 层

**4. 为什么 GetEvents 返回副本？**
   - **封装性**：防止外部直接修改 `eventlog`，破坏数据一致性
   - **防御性编程**：`copy(events, b.eventlog)` 确保内部状态不被意外修改

**5. 为什么 CreateBatch 调用两次 Save？**
   - **第一次 Save**：保存 Batch 的初始状态（pending）
   - **发布 Kafka 事件**：通知其他服务
   - **第二次 Save**：保存事件发布后的状态（确保事件日志被清空）
   - **面试重点**：这样设计是为了实现"恰好一次"语义，避免事件重复发布

### 代码修复经验

**Bug 1：事件类型名称错误**
```go
// ❌ 错误：domain 中定义的是 StatusChanged，不是 BatchStatusChanged
case domain.BatchStatusChanged:

// ✅ 正确：
case domain.StatusChanged:
```

**Bug 2：缺少 String() 调用**
```go
// ❌ 错误：BatchStatus 是自定义类型，不能直接序列化
event.OldStatus, event.NewStatus

// ✅ 正确：调用 String() 方法
event.OldStatus.String(), event.NewStatus.String()
```

**Bug 3：测试包命名**
```go
// ❌ 错误：test/ 目录下的文件不能使用 application 包名
package application

// ✅ 正确：使用 application_test 包名
package application_test
```

**Bug 4：Mock 参数错误**
```go
// ❌ 错误：NewBatchService 只需要 2 个参数
service := application.NewBatchService(mockRepo, mockKafka, mockRepo)

// ✅ 正确：
service := application.NewBatchService(mockRepo, mockKafka)
```

### 已创建/修改的文件
- `internal/application/batch_service.go` - BatchService 实现（4 个方法）
- `internal/messaging/kafka_publisher.go` - Kafka 事件发布器接口
- `internal/infrastructure/kafka/producer.go` - Kafka Producer 实现（3 个方法）
- `internal/application/test/batch_service_test.go` - BatchService 单元测试（6 个测试用例）
- `cmd/test-kafka/main.go` - Kafka 集成测试程序
- `cmd/test-kafka/README.md` - Kafka 测试说明文档
- `docs/Argus_OTA_Platform.md` - 更新架构文档（两阶段上传设计）

### 测试验证
```bash
# 单元测试（6/6 通过）
go test ./internal/application/test/batch_service_test.go -v

# Kafka 集成测试（成功）
go run cmd/test-kafka/main.go

# 验证 Kafka 事件
kafkacat -C -b localhost:9092 -t batch-events -f '%T: %s\n'
```

### 下一步计划
- [ ] 实现 Ingestor HTTP API (cmd/ingestor/main.go)
  - [ ] POST /upload - 流式上传文件到 MinIO
  - [ ] POST /complete - 触发 BatchCreated 事件
- [ ] 实现 Orchestrator Kafka 消费服务 (cmd/orchestrator/main.go)
  - [ ] 消费 BatchCreated 事件
  - [ ] 消费 StatusChanged 事件
  - [ ] 状态机编排（pending → uploaded → scattering）
- [ ] 实现 Redis Barrier（分布式计数器）
- [ ] 实现 C++ Worker（消费 FileScattered 事件）
- [ ] 实现端到端集成测试（Ingestor → Kafka → Orchestrator → Workers）

---

## 2025-01-18 (Day 5)

### 完成事项

#### 1. ✅ 实现 MinIO Client (`internal/infrastructure/minio/client.go`)
- ✅ **NewMinIOClient** - MinIO 客户端初始化
  - 自动创建 Bucket（如果不存在）
  - 完善的错误处理（BucketExists, MakeBucket）
- ✅ **PutObject** - 流式上传方法
  - 使用 `io.Reader` 接口（避免 OOM）
  - PartSize 设为 5MB（大文件自动分片）
  - 返回上传信息（Size, ETag）
- ✅ **零拷贝优化讨论**：
  - 为什么不用 Presigned URL（流程复杂、URL 泄露风险）
  - 为什么使用 io.Copy（自动使用 splice 系统调用）

#### 2. ✅ 实现 HTTP BatchHandler (`internal/interfaces/http/handlers/batch_handler.go`)
- ✅ **CreateBatch** - 创建 Batch API
  - POST /api/v1/batches
  - 参数校验（vehicle_id, vin, expected_workers）
  - 调用 BatchService.CreateBatch
  - 返回 batch_id 和 status

- ✅ **UploadFile** - 文件上传 API（核心）
  - POST /api/v1/batches/:id/files
  - 流式上传（使用 `fileHeader.Open()` 而非 `io.ReadAll`）
  - UUID 生成 fileID（防止文件名冲突）
  - MinIO objectKey 格式：`{batchID}/{fileID}`
  - 调用 BatchService.AddFile 记录文件
  - 返回 file_id 和 size

- ✅ **CompleteUpload** - 完成上传 API
  - POST /api/v1/batches/:id/complete
  - 状态转换：pending → uploaded
  - 触发 BatchCreated 事件（通过 Kafka）

- ✅ **RegisterRoutes** - Gin 路由注册
  - 3 个 API 端点注册
  - 使用 Gin 路由组

#### 3. ✅ 实现 Ingestor 入口 (`cmd/ingestor/main.go`)
- ✅ **Config 结构体** - 配置管理
  - ServerConfig, DatabaseConfig, MinIOConfig, KafkaConfig
  - 从环境变量读取（12-Factor App）

- ✅ **loadConfig** - 配置加载
  - 使用 `getEnv` 辅助函数（提供默认值）
  - 使用 `mustAtoi` 辅助函数（类型转换 + 错误处理）
  - 使用 `parseBool` 辅助函数

- ✅ **initDB** - PostgreSQL 初始化
  - 构建 DSN（Data Source Name）
  - 连接池配置：
    - `SetMaxOpenConns(25)` - 最大打开连接数
    - `SetMaxIdleConns(5)` - 最大空闲连接数
    - `SetConnMaxIdleTime(5 * time.Minute)` - 空闲连接超时
    - `SetConnMaxLifetime(5 * time.Minute)` - 连接最大生命周期
  - Ping 验证连接

- ✅ **initMinIO** - MinIO Client 初始化
  - 调用 `minio.NewMinIOClient`
  - 日志输出

- ✅ **initKafkaProducer** - Kafka Producer 初始化
  - 调用 `kafka.NewKafkaEventProducer`
  - 返回 `messaging.KafkaEventPublisher` 接口

- ✅ **initRouter** - Gin Router 初始化
  - 创建 Gin 实例
  - 初始化 BatchHandler
  - 注册路由

- ✅ **startServer** - HTTP Server 启动
  - 创建 `http.Server` 实例
  - 超时配置：
    - `ReadTimeout: 10s` - 读取请求超时
    - `WriteTimeout: 300s` - 写入响应超时（上传大文件需要长超时）
    - `IdleTimeout: 120s` - 空闲连接超时
  - 在 goroutine 中启动（非阻塞）
  - 返回 server 实例（用于优雅关闭）

- ✅ **gracefulShutdown** - 优雅关闭
  - 监听系统信号（SIGINT, SIGTERM）
  - 30 秒超时 context
  - HTTP Server Shutdown
  - 数据库 Close
  - Kafka Producer Close
  - 日志输出

- ✅ **main** - 主函数
  - 依赖注入链：Config → Infrastructure → Repository → Service → Handler → Router → Server

#### 4. ✅ Bug 修复（8 个）

**MinIO Client Bug（2 个）**
1. ✅ **BucketExists 错误处理** - 添加 `err != nil` 检查
2. ✅ **MakeBucket 错误处理** - 添加 `err != nil` 检查

**BatchHandler Bug（6 个）**
1. ✅ **line 40** - 缺少逗号：`req.VIN, req.ExpectedWorkers`
2. ✅ **line 54** - `c.Params("id")` → `c.Param("id")`（单数）
3. ✅ **line 71** - `&batchID` → `batchID`（不需要取地址）
4. ✅ **line 85** - `batchID` 类型错误（string → uuid.UUID）
5. ✅ **line 96** - receiver 指针缺失：`(h batchHandler)` → `(h *batchHandler)`
6. ✅ **line 102** - 状态名称错误：`BatchStatusCompleted` → `BatchStatusUploaded`

**Ingestor main.go Bug（5 个）**
1. ✅ **line 81** - `mustAtoi("DB_PORT","5432")` → `mustAtoi(getEnv("DB_PORT","5432"), "DB_PORT")`
2. ✅ **line 112** - 缺少 `db.SetMaxOpenConns(25)`
3. ✅ **line 113** - `db.SetMaxIdleConns(25)` → `db.SetMaxIdleConns(5)`
4. ✅ **line 114-115** - `db.SetConnMaxIdleTime(5)` → `db.SetConnMaxIdleTime(5 * time.Minute)`
   - `db.SetConnMaxLifetime(5 & time.Minute)` → `db.SetConnMaxLifetime(5 * time.Minute)`
5. ✅ **line 226** - `startServer(router, cfg.Database.Host)` → `startServer(router, strconv.Itoa(cfg.Server.Port))`

**编译验证**
- ✅ `go build ./cmd/ingestor` 成功
- ✅ 生成二进制文件：`ingestor` (34MB)

#### 5. ✅ AI Agent Worker 架构设计 (`docs/ai-agent-architecture.md`)
- ✅ **DDD 分层设计** - 完整的目录结构和职责划分
  - Domain 层：Diagnosis, Prompt, TokenUsage
  - Application 层：DiagnoseService, PromptBuilder, SummaryPruner, TokenTracker
  - Infrastructure 层：EinoClient, VectorRetriever, DiagnosisRepository
  - Interfaces 层：HTTP Handler（可选）

- ✅ **核心流程定义** - 诊断流程的 9 个步骤
  1. Token 检查（每日限额）
  2. 读取聚合数据
  3. Summary 剪枝（减少 Token）
  4. RAG 检索（历史相似案例）
  5. 构造 Prompt
  6. 调用 LLM（Eino）
  7. Token 追踪
  8. 保存结果
  9. 发布事件

- ✅ **接口定义**
  - `LLMClient` - LLM 客户端接口（Diagnose, GetEmbedding, Close）
  - `VectorRetriever` - RAG 检索接口（Retrieve, Index）
  - `DiagnosisRepository` - 诊断结果仓储接口（Save, FindByID, FindByBatchID, FindAggregatedData）

- ✅ **数据模型**
  - `Diagnosis` - 诊断结果聚合根
  - `Summary` - 剪枝后的数据摘要（Top-K 异常码）
  - `TokenUsage` - Token 使用记录（PromptTokens, CompletionTokens, TotalTokens, EstimatedCost）
  - `SimilarCase` - 相似案例（ID, Diagnosis, Distance）

- ✅ **Token 成本控制策略**
  - Summary 剪枝（Top-K 异常码，默认 K=10）
  - Prompt 优化（简洁 + Few-shot 精简）
  - 每日限额（10 万 Token）
  - Token 追踪（记录每日成本）
  - 降级策略（Token 超限返回 Top-K 异常码）

- ✅ **RAG 检索设计**
  - pgvector 向量数据库（与 PostgreSQL 集成）
  - OpenAI Embedding API（Ada Embedding V2，1536 维度）
  - 相似度搜索（<=> 操作符）
  - 增量索引（新诊断自动索引）

- ✅ **开发策略** - 6 个阶段，6-9 天工作量
  - 阶段 1: 基础框架（1-2 天）
  - 阶段 2: 数据层（1 天）
  - 阶段 3: LLM 集成（1-2 天）
  - 阶段 4: Token 控制（0.5 天）
  - 阶段 5: RAG 检索（1-2 天）
  - 阶段 6: 测试与优化（1-2 天）

- ✅ **技术栈选择**
  - LLM 框架：Eino（Go 原生、轻量级、高性能）
  - LLM Provider：OpenAI GPT-4o（性能强、成本可控）
  - 向量数据库：pgvector（与 PostgreSQL 集成、无需额外部署）
  - Embedding：OpenAI Ada Embedding V2（1536 维度、性能好）

#### 6. ✅ 文档更新
- ✅ `LEARNING_LOG.md` - 今日学习日志（300 行）
  - 完成功能与技术选型
  - 5 个面试高频考点（零拷贝、优雅关闭、连接池、DDD）
  - 8 个踩坑案例
  - 下一步计划

- ✅ `PROGRESS.md` - 系统进度清单（已更新）
  - Ingestor: 0% → 100% ✅
  - Workers: 0% → 5%（AI Agent 架构设计完成）
  - 文档: 30% → 40%
  - Bug 已修复：8 个
  - 总体进度: 20%

### 核心理解：接入层（Ingestor）设计原则

**1. 依赖注入链**
```
Config → Infrastructure → Repository → Service → Handler → Router → Server
```
- 每一层只依赖下一层的接口（依赖倒置）
- cmd 层只负责启动，不包含业务逻辑
- 可以轻松替换实现（PostgreSQL → MySQL）

**2. 流式上传**
```go
// ✅ 正确：流式上传
file, _ := fileHeader.Open()
defer file.Close()
minioClient.PutObject(ctx, objectKey, file, size, contentType)

// ❌ 错误：缓存整个文件（OOM）
data, _ := io.ReadAll(file)
minioClient.PutObject(ctx, objectKey, bytes.NewReader(data), size, contentType)
```

**3. 优雅关闭**
```go
// 1. 监听系统信号
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
<-sigCh

// 2. 设置超时（避免永久阻塞）
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// 3. 关闭服务（按顺序）
server.Shutdown(ctx)  // 等待请求完成
db.Close()            // 关闭数据库
kafkaProducer.Close() // 关闭 Kafka
```

### 技术决策与面试重点

**1. 为什么用 Gin 而不是标准库？**
   - **路由简洁**：`r.POST("/batches/:id/files", h.UploadFile)`
   - **中间件丰富**：Logger, Recovery, CORS
   - **性能优秀**：比标准库快 10 倍
   - **社区活跃**：GitHub 70k+ stars

**2. 为什么流式上传？**
   - **避免 OOM**：大文件（GB 级）不会占用大量内存
   - **减少 GC 压力**：不需要分配大块内存
   - **性能更好**：边读边传，延迟更低

**3. 零拷贝 vs 流式传输？**
   - **零拷贝**：磁盘 → 内核态 → 网卡（2 次拷贝）
   - **流式传输**：用户态内存拷贝 + io.Copy 优化（splice）
   - **MinIO SDK**：已经使用 io.Copy（自动优化）
   - **完全零拷贝**：使用 Presigned URL（客户端直传 MinIO）

**4. 为什么数据库连接池需要 MaxIdleConns？**
   - **避免资源浪费**：空闲连接占用数据库资源
   - **提高性能**：保持少量空闲连接，避免频繁建立连接
   - **最佳实践**：MaxIdleConns < MaxOpenConns（如 5 < 25）

**5. 为什么 WriteTimeout 是 300s？**
   - **上传大文件**：GB 级文件需要长时间上传
   - **避免超时**：网络慢时不会中断上传
   - **ReadTimeout 短**：10s（防止慢速攻击）

**6. Eino vs LangChain？**
   - **Eino**：Go 原生、轻量级、高性能、适合高并发
   - **LangChain**：Python 生态、功能丰富、但性能差
   - **技术栈统一**：Eino 与 Orchestrator/Workers 技术栈一致

### 代码修复经验

**Bug 1：c.Params vs c.Param**
```go
// ❌ 错误：c.Params 返回 Params 类型
batchID := c.Params("id")

// ✅ 正确：c.Param 返回 string
batchID := c.Param("id")
```

**Bug 2：类型不匹配**
```go
// ❌ 错误：batchID 是 string，但 AddFile 期望 uuid.UUID
batchID := c.Param("id")
batchService.AddFile(ctx, batchID, fileID)

// ✅ 正确：解析 UUID
batchIDStr := c.Param("id")
batchID, err := uuid.Parse(batchIDStr)
if err != nil {
    return c.JSON(400, gin.H{"error": "invalid batch id"})
}
batchService.AddFile(ctx, batchID, fileID)
```

**Bug 3：receiver 指针缺失**
```go
// ❌ 错误：Method receiver 应该是指针
func (h batchHandler) CompleteUpload(c *gin.Context) { ... }

// ✅ 正确：
func (h *batchHandler) CompleteUpload(c *gin.Context) { ... }
```

**Bug 4：环境变量读取错误**
```go
// ❌ 错误：直接传字符串，没有读取环境变量
Port: mustAtoi("DB_PORT", "5432")

// ✅ 正确：先读取环境变量，再转换
Port: mustAtoi(getEnv("DB_PORT", "5432"), "DB_PORT")
```

**Bug 5：连接池配置错误**
```go
// ❌ 错误：类型不匹配（int ≠ time.Duration）
db.SetConnMaxIdleTime(5)

// ✅ 正确：
db.SetConnMaxIdleTime(5 * time.Minute)
```

**Bug 6：运算符错误**
```go
// ❌ 错误：& 是取地址运算符，不是乘法
db.SetConnMaxLifetime(5 & time.Minute)

// ✅ 正确：
db.SetConnMaxLifetime(5 * time.Minute)
```

### 已创建/修改的文件

**新增文件（7 个）**
- `internal/infrastructure/minio/client.go` (41 行)
- `internal/interfaces/http/handlers/batch_handler.go` (120 行)
- `cmd/ingestor/main.go` (230 行)
- `LEARNING_LOG.md` (300 行)
- `PROGRESS.md` (已更新)
- `docs/ai-agent-architecture.md` (500 行)
- `docs/development-log.md` (已追加)

**修改文件（1 个）**
- `go.mod` - 添加 Gin 和 MinIO SDK 依赖
  - `github.com/gin-gonic/gin v1.11.0`
  - `github.com/minio/minio-go/v7 v7.0.98`

### 代码统计

| 模块 | 文件数 | 代码行数 | 完成度 |
|------|--------|----------|--------|
| Domain | 7 | ~500 | 70% |
| Infrastructure | 3 | ~300 | 40% |
| Application | 5 | ~200 | 50% |
| Interfaces | 1 | ~120 | 40% |
| cmd/ingestor | 1 | ~230 | 100% ✅ |
| docs/ | 4 | ~1200 | 40% |
| **总计** | **21** | **~2550** | **20%** |

### 下一步计划

#### 🔥 高优先级（本周完成）
1. **PostgreSQL Migration**（30 分钟）
   - 创建 `batches` 表
   - 创建 `files` 表
   - 创建索引

2. **Docker Compose**（1 小时）
   - 搭建本地开发环境
   - 验证所有服务启动

3. **端到端测试**（1 小时）
   - 启动所有服务
   - 测试上传文件流程
   - 验证 Kafka 事件

#### 📅 中优先级（下周完成）
4. **Orchestrator Service**（2-3 天）
   - Kafka Consumer
   - 状态机驱动
   - Redis Barrier 协调

5. **C++ Worker**（2-3 天）
   - rec 文件解析
   - Kafka 集成

6. **Python Aggregator**（2-3 天）
   - 数据聚合
   - Top-K 计算
   - Kafka 集成

#### 🔮 低优先级（后续迭代）
7. **AI Agent Worker**（6-9 天）- 架构设计完成 ✨
   - 阶段 1: 基础框架（1-2 天）
   - 阶段 2: 数据层（1 天）
   - 阶段 3: LLM 集成（1-2 天）
   - 阶段 4: Token 控制（0.5 天）
   - 阶段 5: RAG 检索（1-2 天）
   - 阶段 6: 测试与优化（1-2 天）

8. **Query Service + Singleflight**（1 天）
9. **SSE 实时推送**（1 天）

### 面试重点（AI 模块）

**Q: 如何控制 LLM Token 成本？**
A:
1. **Summary 剪枝** - 只保留 Top-K 异常码（K=10）
2. **每日限额** - 设置 10 万 Token 上限
3. **降级策略** - Token 超限返回 Top-K 异常码
4. **缓存机制** - 相似诊断结果复用

**Q: RAG 如何实现？**
A:
1. **Embedding API** - 文本 → 向量（OpenAI Ada Embedding V2）
2. **pgvector 存储** - 向量 + 诊断结果
3. **相似度搜索** - `<=>` 操作符（余弦距离）
4. **Top-K 检索** - 返回最相似的 5 个案例

**Q: 为什么用 Eino 而不是 LangChain？**
A:
1. **Go 原生** - 与 Orchestrator/Workers 技术栈一致
2. **轻量级** - 比 LangChain 简单
3. **高性能** - 适合高并发场景
4. **内置 Token 追踪** - 自动记录 Token 使用

**Q: 如何保证 LLM 调用的可靠性？**
A:
1. **重试机制** - 指数退避（3 次）
2. **超时控制** - 30 秒超时
3. **降级策略** - Token 超限返回 Top-K 异常码
4. **错误日志** - 记录所有失败调用

### 今日总结

**完成量**：
- 新增代码：~391 行（不含文档）
- 新增文档：~1200 行
- 修复 Bug：13 个（MinIO 2 + BatchHandler 6 + Ingestor 5）
- 编译验证：✅ 通过

**核心成果**：
- ✅ **Ingestor（接入层）** - 完整实现并编译通过
- ✅ **AI Agent Worker 架构** - 完整设计文档，开发策略清晰
- ✅ **Bug 修复** - 13 个 Bug 全部修复

**技术收获**：
- Gin 框架使用（路由、中间件、文件上传）
- MinIO 流式上传（io.Reader、PartSize）
- 依赖注入模式（Config → Infrastructure → Service → Handler）
- 优雅关闭（系统信号、context 超时、资源释放）
- 零拷贝优化（splice、sendfile、io.Copy）
- AI 架构设计（Eino、RAG、pgvector、Token 控制）

**明天目标**：
- PostgreSQL Migration（创建 batches、files 表）
- Docker Compose（搭建本地开发环境）
- 端到端测试（验证上传流程）

---
