# Kafka Integration Test

## 快速开始

### 1. 启动基础设施

```bash
cd /Users/xuewentao/my_project/argus-ota-platform
docker-compose up -d postgres redis zookeeper kafka minio
```

等待所有服务启动完成（大约 10-15 秒）

### 2. 运行测试程序

```bash
go run cmd/test-kafka/main.go
```

### 3. 查看 Kafka 事件

#### 方法 1: 使用 kafkacat（推荐）

```bash
kafkacat -C -b localhost:9092 -t batch-events -f '%T: %s\n'
```

#### 方法 2: 使用 kafka-console-consumer

```bash
docker exec -it argus-kafka kafka-console-consumer \
  --bootstrap-server localhost:9092 \
  --topic batch-events \
  --from-beginning
```

#### 方法 3: 查看日志

运行测试程序时，你会看到类似的输出：

```
=== Argus OTA Platform - Kafka Integration Test ===
✅ Database connected successfully
[Kafka] Producer created successfully. Brokers: [localhost:9092], Topic: batch-events
✅ Kafka producer created successfully

--- Test 1: Create Batch ---
[Kafka] Publishing 1 events to topic: batch-events
[Kafka] BatchCreated sent successfully. Partition: 0, Offset: 0
[Kafka] Successfully published 1 events
✅ Batch created: ID=xxx, Status=pending, VehicleID=vehicle-001

--- Test 2: Transition Status ---
[Kafka] Publishing 1 events to topic: batch-events
[Kafka] StatusChanged sent successfully. Partition: 0, Offset: 1
[Kafka] Successfully published 1 events
✅ Status transitioned: pending → uploaded
...

=== All tests completed successfully! ===
```

## 预期的事件格式

### BatchCreated 事件
```json
{"event_type":"BatchCreated","batch_id":"xxx","vehicle_id":"vehicle-001","vin":"VIN123456789","timestamp":"2026-01-18T12:00:00Z"}
```

### StatusChanged 事件
```json
{"event_type":"StatusChanged","batch_id":"xxx","old_status":"pending","new_status":"uploaded","timestamp":"2026-01-18T12:00:01Z"}
```

## 故障排查

### 问题 1: 连接数据库失败

**错误**：`Failed to connect to database: connection refused`

**解决**：
```bash
# 检查 PostgreSQL 是否运行
docker ps | grep postgres

# 查看日志
docker logs argus-postgres
```

### 问题 2: Kafka 连接失败

**错误**：`Failed to create Kafka producer: dial tcp: connection refused`

**解决**：
```bash
# 检查 Kafka 是否运行
docker ps | grep kafka

# 查看日志
docker logs argus-kafka

# 重启 Kafka
docker-compose restart kafka
```

### 问题 3: 没有看到 Kafka 事件

**可能原因**：
1. Kafka 还没完全启动（等待 10 秒）
2. Topic 不存在

**解决**：
```bash
# 列出所有 topic
docker exec argus-kafka kafka-topics --bootstrap-server localhost:9092 --list

# 如果 topic 不存在，创建它
docker exec argus-kafka kafka-topics --bootstrap-server localhost:9092 --create --topic batch-events --partitions 1 --replication-factor 1
```

## 架构图

```
┌─────────────────────────────────────────┐
│  Test Program (cmd/test-kafka/main.go)   │
│  - 创建 BatchService                      │
│  - 注入 Repository + Kafka               │
│  - 调用业务方法                           │
└──────────────┬──────────────────────────┘
               │
               ↓
┌─────────────────────────────────────────┐
│  BatchService                            │
│  - CreateBatch()                         │
│  - TransitionBatchStatus()               │
└──────────────┬──────────────────────────┘
               │
        ┌──────┴──────┐
               │
               ↓
         ┌─────────┐
         │  Kafka  │ ← 事件发布
         └─────────┘
```

## 下一步

1. ✅ Kafka 集成完成
2. 🔄 实现完整的单元测试
3. 🔄 实现 HTTP API (Gin Router)
4. 🔄 实现 Redis Barrier
