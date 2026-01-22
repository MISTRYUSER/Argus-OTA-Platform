# Argus OTA Platform - 剩余工作清单

**更新日期**: 2026-01-21
**系统完整度**: 50%
**策略**: 先完成基础架构，eino 放到最后

---

## ✅ 已完成功能（Day 1-8）

### Domain 层 (70%)
- ✅ BatchStatus 状态机（8 个状态 + 转换规则）
- ✅ ProcessingStatus 状态机（5 个状态 + 转换规则）
- ✅ Batch 聚合根（6 个方法）
- ✅ File 聚合根（基础结构）
- ✅ 领域事件（BatchCreated, StatusChanged, FileParsed）

### Application 层 (70%)
- ✅ BatchService（CreateBatch, TransitionBatchStatus, AddFile）
- ✅ OrchestrateService（事件路由、状态机、Redis Barrier）
- ⬜ QueryService（报告查询 + Singleflight）

### Infrastructure 层 (75%)
- ✅ PostgreSQL Repository（5 个方法）
- ✅ Redis Client（7 个方法 + Pipeline）
- ✅ Kafka Producer（3 个事件类型支持）
- ✅ Kafka Consumer（Consumer Group + 手动提交）
- ✅ MinIO Client（流式上传）

### Interfaces 层 (45%)
- ✅ HTTP Handler（CreateBatch, UploadFile, CompleteUpload）
- ⬜ Query Handler（报告查询）
- ⬜ SSE Handler（基础实现，后续迁移到 eino）

### cmd/ (70%)
- ✅ cmd/ingestor/main.go（完整实现）
- ✅ cmd/orchestrator/main.go（完整实现）
- ✅ cmd/mock-cpp-worker/main.go（完整实现）
- ⬜ cmd/query-service/main.go

### 测试验证 (60%)
- ✅ Worker 单元测试（10/10 FileParsed 事件）
- ✅ 完整流程测试（6/6 核心步骤）
- ✅ Redis Barrier 验证（SADD + SCARD）
- ⬜ 端到端集成测试

---

## 🔥 高优先级剩余工作（Day 9-12）

### 1. Query Service + Singleflight (2 天)

**目标**: 实现高并发报告查询，防止缓存击穿

**任务**:
- [ ] 实现 `internal/application/query_service.go`
  ```go
  type QueryService struct {
      sf       singleflight.Group
      repo     ReportRepository
      cache    CacheClient
  }

  func (s *QueryService) GetReport(ctx context.Context, batchID uuid.UUID) (*Report, error) {
      result, err, _ := s.sf.Do(batchID.String(), func() (interface{}, error) {
          // 1. 查询缓存
          report, err := s.cache.Get(ctx, batchID)
          if err == nil {
              return report, nil
          }

          // 2. 查询数据库
          report, err = s.repo.FindByID(ctx, batchID)
          if err != nil {
              return nil, err
          }

          // 3. 写入缓存
          s.cache.Set(ctx, batchID, report, 10*time.Minute)
          return report, nil
      })

      return result.(*Report), err
  }
  ```

- [ ] 实现 `internal/interfaces/http/handlers/query_handler.go`
  - `GET /api/v1/batches/:id/report` - 报告查询
  - `GET /api/v1/batches/:id/progress` - 进度查询

- [ ] 实现 `cmd/query-service/main.go`
  - 依赖注入：Config → Redis → PostgreSQL → QueryService → Handler
  - 优雅关闭：SIGINT/SIGTERM

**验证目标**:
- 100 并发查询 → 1 次数据库查询
- 缓存命中率 > 90%

**面试考点**:
- **Q: 什么是缓存击穿？**
  - A: 热点 key 过期，大量并发直接打到数据库
- **Q: Singleflight 如何解决？**
  - A: 相同 key 的并发请求合并为 1 次

---

### 2. 修复状态转换流程 (1-2 天)

**问题**: 当前 TotalFiles = 0，状态转换卡在 scattering

**解决方案**:
- [ ] **步骤 1**: 修改 Worker，从数据库查询 TotalFiles
  ```go
  func (w *Worker) handleBatchCreated(ctx context.Context, event map[string]interface{}) error {
      batchIDStr := event["batch_id"].(string)
      batchID, _ := uuid.Parse(batchIDStr)

      // 查询 Batch TotalFiles
      batch, err := w.batchRepo.FindByID(ctx, batchID)
      if err != nil {
          return fmt.Errorf("failed to find batch: %w", err)
      }

      // 发布对应数量的 FileParsed 事件
      var events []domain.DomainEvent
      for i := 0; i < batch.TotalFiles; i++ {
          events = append(events, domain.FileParsed{
              BatchID:   batchID,
              FileID:    uuid.New(),
              OccurredAt: time.Now(),
          })
      }

      return w.kafka.PublishEvents(ctx, events)
  }
  ```

- [ ] **步骤 2**: Worker 注入 BatchRepository
  ```go
  type Worker struct {
      kafka messaging.KafkaEventPublisher
      batchRepo domain.BatchRepository  // 新增
  }

  func NewWorker(kafka messaging.KafkaEventPublisher, batchRepo domain.BatchRepository) *Worker {
      return &Worker{
          kafka:     kafka,
          batchRepo: batchRepo,
      }
  }
  ```

- [ ] **步骤 3**: 修改 Worker main.go
  ```go
  func main() {
      // ... 初始化 PostgreSQL
      db := initDB()

      // 初始化 BatchRepository
      batchRepo := postgres.NewBatchRepository(db)

      // 创建 Worker（注入 Repository）
      worker := NewWorker(kafkaProducer, batchRepo)

      // ... 启动 Worker
  }
  ```

- [ ] **步骤 4**: 验证完整流程
  - scattering → scattered (所有文件解析完成)
  - scattered → gathering (触发下一步)
  - gathering → gathered (聚合完成)
  - gathered → diagnosing (触发 AI)
  - diagnosing → completed (诊断完成)

---

### 3. 端到端集成测试 (1-2 天)

**目标**: 验证完整流程从头到尾

**任务**:
- [ ] 创建 `tests/e2e/full_flow_test.go`
  ```go
  func TestFullFlow(t *testing.T) {
      // 1. 创建 Batch
      batch := createBatch(t, "TEST-001", "TESTVIN001")

      // 2. 上传文件（通过 API）
      uploadFile(t, batch.ID, "test.log")
      uploadFile(t, batch.ID, "test2.log")

      // 3. 完成上传
      completeUpload(t, batch.ID)

      // 4. 等待状态转换（使用轮询）
      waitForStatus(t, batch.ID, "pending", 1*time.Second)
      waitForStatus(t, batch.ID, "uploaded", 1*time.Second)
      waitForStatus(t, batch.ID, "scattering", 1*time.Second)
      waitForStatus(t, batch.ID, "scattered", 10*time.Second)  // Worker 处理需要时间
      waitForStatus(t, batch.ID, "gathering", 1*time.Second)
      waitForStatus(t, batch.ID, "gathered", 5*time.Second)

      // 5. 查询报告
      report := getReport(t, batch.ID)
      assert.NotNil(t, report)
      assert.Equal(t, batch.ID, report.BatchID)
  }
  ```

- [ ] 性能测试
  ```go
  func TestConcurrentUpload(t *testing.T) {
      // 100 个并发 Batch 上传
      var wg sync.WaitGroup
      for i := 0; i < 100; i++ {
          wg.Add(1)
          go func(idx int) {
              defer wg.Done()
              createBatch(t, fmt.Sprintf("CONCURRENT-%d", idx))
          }(i)
      }
      wg.Wait()
  }
  ```

- [ ] 故障恢复测试
  - Worker 崩溃恢复
  - Kafka 消息重复消费（验证幂等性）
  - Redis 连接断开重连

---

## 📅 中优先级工作（Day 13-15）

### 4. SSE 实时进度推送（基础实现）(1-2 天)

**目标**: 先实现基础 SSE，后续迁移到 eino

**任务**:
- [ ] 实现 Redis Pub/Sub 进度广播
  ```go
  func (s *OrchestrateService) PublishProgress(ctx context.Context, batchID uuid.UUID, progress int) {
      channel := fmt.Sprintf("batch:%s:progress", batchID)
      message := fmt.Sprintf(`{"batch_id":"%s","progress":%d}`, batchID, progress)
      s.redis.Publish(ctx, channel, message)
  }
  ```

- [ ] 实现 SSE Handler
  ```go
  func (h *SSEHandler) StreamProgress(c *gin.Context) {
      batchID := c.Param("id")

      // 设置 SSE 响应头
      c.Writer.Header().Set("Content-Type", "text/event-stream")
      c.Writer.Header().Set("Cache-Control", "no-cache")
      c.Writer.Header().Set("Connection", "keep-alive")

      // 订阅 Redis Pub/Sub
      pubsub := redisClient.Subscribe(ctx, fmt.Sprintf("batch:%s:progress", batchID))
      defer pubsub.Close()

      // 流式推送
      for {
          msg, err := pubsub.ReceiveMessage(ctx)
          if err != nil {
              break
          }

          fmt.Fprintf(c.Writer, "data: %s\n\n", msg.Payload)
          c.Writer.Flush()
      }
  }
  ```

- [ ] 添加进度广播点
  - FileParsed 事件处理时：PublishProgress(batchID, processedCount)
  - 状态转换时：PublishProgress(batchID, newStatus)

**注意**: 这是临时实现，后续会迁移到 eino

---

### 5. Python Worker 实现 (2-3 天)

**目标**: 实现 Gather 阶段的数据聚合

**任务**:
- [ ] 创建 `workers/python-aggregator/main.py`
  ```python
  from confluent_kafka import Consumer, Producer
  from minio import Minio
  import pandas as pd

  def main():
      # Kafka Consumer
      consumer = Consumer({
          'bootstrap.servers': 'localhost:9092',
          'group.id': 'python-aggregator-group',
          'auto.offset.reset': 'earliest'
      })
      consumer.subscribe(['batch-events'])

      # MinIO Client
      minio = Minio('localhost:9000', ...)

      # Kafka Producer
      producer = Producer({'bootstrap.servers': 'localhost:9092'})

      while True:
          msg = consumer.poll(1.0)
          if msg is None:
              continue

          event = json.loads(msg.value())
          if event['event_type'] == 'AllFilesScattered':
              # 聚合数据
              aggregate_data(event['batch_id'])
              # 发布事件
              producer.produce('batch-events', json.dumps({
                  'event_type': 'AllFilesGathered',
                  'batch_id': event['batch_id'],
                  ...
              }))
  ```

- [ ] 实现 AllFilesGathered 事件
  ```go
  type AllFilesGathered struct {
      BatchID     uuid.UUID
      TotalLines  int64
      ErrorCodes  map[string]int
      OccurredAt  time.Time
  }
  ```

---

## 🚀 低优先级工作（Day 16+）

### 6. AI Diagnose (使用 eino) (2-3 天)

**任务**:
- [ ] 安装 eino
  ```bash
  go get github.com/cloudwego/eino
  ```

- [ ] 使用 eino LLM API
  ```go
  import "github.com/cloudwego/eino/components/model/openai"

  llm := openai.NewLLM(openai.Config{
      APIKey: os.Getenv("OPENAI_API_KEY"),
  })

  prompt := eino.NewPrompt(
      "你是 OTA 日志诊断专家",
      "分析以下数据...",
  )

  response, err := llm.Generate(ctx, prompt)
  ```

- [ ] 迁移 SSE 到 eino
  ```go
  // 替换现有的 SSE 实现
  stream := eino.NewStream()
  stream.Write(...)
  ```

---

### 7. 监控与运维 (1-2 天)

**任务**:
- [ ] Prometheus metrics
  ```go
  import "github.com/prometheus/client_golang/prometheus"

  var (
      batchCreatedTotal = prometheus.NewCounter(...)
      fileProcessedTotal = prometheus.NewCounter(...)
      kafkaConsumerLag = prometheus.NewGauge(...)
  )
  ```

- [ ] Docker Compose 生产配置
  - 资源限制
  - 健康检查
  - 日志轮转

---

## 📊 工作量评估

| 模块 | 工作量 | 优先级 | 备注 |
|------|--------|--------|------|
| Query Service + Singleflight | 2 天 | 🔥 高 | 防缓存击穿 |
| 状态转换修复 | 1-2 天 | 🔥 高 | TotalFiles 查询 |
| 端到端测试 | 1-2 天 | 🔥 高 | 质量保证 |
| SSE 基础实现 | 1-2 天 | 📅 中 | 临时实现 |
| Python Worker | 2-3 天 | 📅 中 | Gather 阶段 |
| AI Diagnose (eino) | 2-3 天 | 🚀 低 | 最后做 |
| 监控运维 | 1-2 天 | 🚀 低 | 生产就绪 |

**总工作量**: 10-14 天

---

## 🎯 更新的里程碑

- [x] **Milestone 1**: 基础架构完成（Day 1-7）✅
- [x] **Milestone 2**: Worker 实现与测试（Day 8）✅
- [ ] **Milestone 3**: 查询 + 状态转换修复（Day 9-12）
- [ ] **Milestone 4**: Gather 阶段 + AI（Day 13-16）
- [ ] **Milestone 5**: 生产就绪（Day 17+）

---

## 📝 技术债务

### 需要改进的地方

1. **Worker TotalFiles 查询** 🔥
   - 当前：硬编码 2 个文件
   - 改进：从数据库查询真实 TotalFiles
   - 优先级：高

2. **状态转换完成** 🔥
   - 当前：卡在 scattering
   - 改进：完成所有状态转换
   - 优先级：高

3. **单元测试覆盖**
   - 当前：只有 BatchService 测试
   - 改进：添加 OrchestrateService、QueryService 测试
   - 优先级：中

4. **监控与日志**
   - 当前：只有基础日志
   - 改进：添加 Prometheus metrics
   - 优先级：低

---

**备注**:
- 当前系统完整度：50%
- 核心架构已验证：✅
- 策略：先完成基础架构，eino 放到最后
- 预计完成时间：10-14 天
