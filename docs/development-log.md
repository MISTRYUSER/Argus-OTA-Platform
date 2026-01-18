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
