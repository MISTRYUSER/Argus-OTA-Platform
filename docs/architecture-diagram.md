# Argus OTA Platform - 架构图

**使用 Mermaid 绘制的系统架构**

---

## 1. 系统整体架构图

```mermaid
graph TB
    subgraph "客户端层"
        Vehicle[车辆端]
        Admin[管理后台]
        API[API Consumer]
    end

    subgraph "接入层 (Gin)"
        Ingestor[Ingestor<br/>cmd/ingestor/main.go<br/>✅ 已完成]
        QueryService[Query Service<br/>cmd/query-service/main.go<br/>⬜ 待实现]
    end

    subgraph "存储层"
        MinIO[MinIO<br/>对象存储<br/>✅ 已完成]
        PostgreSQL[(PostgreSQL<br/>关系型数据库<br/>✅ 已完成)]
        Redis[(Redis<br/>缓存/Barrier<br/>✅ 已完成)]
    end

    subgraph "消息层"
        Kafka[Kafka<br/>事件总线<br/>✅ 已完成]
    end

    subgraph "编排层"
        Orchestrator[Orchestrator<br/>cmd/orchestrator/main.go<br/>✅ 已完成]
    end

    subgraph "Worker层"
        CppWorker[Mock C++ Worker<br/>cmd/mock-cpp-worker/main.go<br/>✅ 已完成]
        PythonWorker[Python Worker<br/>⬜ 待实现]
        AIAgent[AI Agent<br/>⬜ 待实现<br/>计划使用 eino]
    end

    Vehicle -->|HTTP Stream| Ingestor
    Admin -->|REST API| QueryService
    API -->|REST API| QueryService

    Ingestor -->|流式上传| MinIO
    Ingestor -->|保存状态| PostgreSQL
    Ingestor -->|发布事件| Kafka

    Kafka -->|消费事件| Orchestrator
    Kafka -->|消费事件| CppWorker
    Kafka -->|消费事件| PythonWorker
    Kafka -->|消费事件| AIAgent

    Orchestrator -->|读写状态| PostgreSQL
    Orchestrator -->|Barrier计数| Redis
    Orchestrator -->|发布事件| Kafka

    CppWorker -->|下载文件| MinIO
    CppWorker -->|发布事件| Kafka

    QueryService -->|查询报告| PostgreSQL
    QueryService -->|缓存| Redis

    PythonWorker -->|发布事件| Kafka
    AIAgent -->|发布事件| Kafka

    style Ingestor fill:#90EE90
    style Orchestrator fill:#90EE90
    style CppWorker fill:#90EE90
    style QueryService fill:#FFB6C1
    style PythonWorker fill:#FFB6C1
    style AIAgent fill:#FFB6C1
```

---

## 2. 数据流向图（完整流程）

```mermaid
sequenceDiagram
    autonumber
    participant Vehicle as 车辆端
    participant Ingestor as Ingestor (Gin)
    participant MinIO as MinIO
    participant Kafka as Kafka
    participant Orchestrator as Orchestrator
    participant Worker as C++ Worker
    participant Redis as Redis Barrier
    participant DB as PostgreSQL

    Note over Vehicle,DB: 阶段1：文件上传
    Vehicle->>Ingestor: POST /api/v1/batches (创建Batch)
    Ingestor->>DB: INSERT INTO batches (status=pending)
    Ingestor-->>Vehicle: 返回 batch_id

    Vehicle->>Ingestor: POST /api/v1/batches/:id/files (上传文件1)
    Ingestor->>MinIO: Stream rec file (流式上传)
    Ingestor->>DB: UPDATE batches SET total_files=total_files+1
    Ingestor-->>Vehicle: 返回 file_id

    Vehicle->>Ingestor: POST /api/v1/batches/:id/files (上传文件2)
    Ingestor->>MinIO: Stream rec file (流式上传)
    Ingestor->>DB: UPDATE batches SET total_files=total_files+1
    Ingestor-->>Vehicle: 返回 file_id

    Vehicle->>Ingestor: POST /api/v1/batches/:id/complete (完成上传)
    Ingestor->>DB: UPDATE status=pending→uploaded
    Ingestor->>Kafka: Publish BatchCreated 事件

    Note over Vehicle,DB: 阶段2：异步处理
    Kafka->>Orchestrator: Consume BatchCreated
    Orchestrator->>DB: UPDATE status=uploaded→scattering
    Orchestrator->>Kafka: Publish StatusChanged (scattering)

    Kafka->>Worker: Consume BatchCreated
    Worker->>DB: Query TotalFiles
    Worker->>MinIO: Download rec files

    par 并行处理多个文件
        Worker->>Worker: Parse file 1 (2秒)
        Worker->>Worker: Parse file 2 (2秒)
    end

    Worker->>Kafka: Publish FileParsed 事件 (×2)

    Note over Vehicle,DB: 阶段3：Barrier协调
    Kafka->>Orchestrator: Consume FileParsed #1
    Orchestrator->>Redis: SADD processed_files file_id1
    Redis-->>Orchestrator: count=1

    Kafka->>Orchestrator: Consume FileParsed #2
    Orchestrator->>Redis: SADD processed_files file_id2
    Redis-->>Orchestrator: count=2

    Orchestrator->>Orchestrator: Check count==totalFiles? (2==2✅)
    Orchestrator->>Redis: DELETE processed_files
    Orchestrator->>DB: UPDATE status=scattering→scattered
    Orchestrator->>Kafka: Publish StatusChanged (scattered)
```

---

## 3. DDD 分层架构图

```mermaid
graph TB
    subgraph "接口层 (Interfaces)"
        HTTP[HTTP Handlers<br/>✅ batch_handler.go<br/>⬜ query_handler.go]
        SSE[SSE Handler<br/>⬜ 待实现]
    end

    subgraph "应用层 (Application)"
        BatchService[BatchService<br/>✅ CreateBatch<br/>✅ AddFile<br/>✅ TransitionBatchStatus]
        OrchestrateService[OrchestrateService<br/>✅ 事件路由<br/>✅ 状态机<br/>✅ Redis Barrier]
        QueryService[QueryService<br/>⬜ GetReport<br/>⬜ Singleflight]
    end

    subgraph "领域层 (Domain)"
        Batch[Batch 聚合根<br/>✅ 状态转换<br/>✅ 业务规则]
        File[File 聚合根<br/>✅ 基础结构]
        Events[领域事件<br/>✅ BatchCreated<br/>✅ StatusChanged<br/>✅ FileParsed]
        Status[BatchStatus<br/>✅ 状态机<br/>✅ 8个状态]
        Repos[Repository 接口<br/>✅ BatchRepository<br/>✅ FileRepository]
    end

    subgraph "基础设施层 (Infrastructure)"
        KafkaProd[Kafka Producer<br/>✅ 3种事件]
        KafkaCons[Kafka Consumer<br/>✅ Consumer Group]
        RedisClient[Redis Client<br/>✅ 7个方法<br/>✅ Pipeline]
        PostgresRepo[PostgreSQL Repo<br/>✅ 5个方法]
        MinioClient[MinIO Client<br/>✅ 流式上传]
    end

    HTTP -->|调用| BatchService
    SSE -->|调用| QueryService

    BatchService -->|使用| Batch
    OrchestrateService -->|使用| Batch
    OrchestrateService -->|协调| Events
    QueryService -->|查询| Batch

    Batch -->|定义| Repos
    Events -->|实现| Repos

    BatchService -->|依赖| Repos
    OrchestrateService -->|依赖| Repos
    QueryService -->|依赖| Repos

    BatchService -->|使用| KafkaProd
    OrchestrateService -->|使用| KafkaProd
    OrchestrateService -->|使用| KafkaCons
    OrchestrateService -->|使用| RedisClient
    QueryService -->|使用| RedisClient

    Repos -.->|实现| PostgresRepo
    Repos -.->|实现| MinioClient

    style Batch fill:#FFE4B5
    style Events fill:#FFE4B5
    style Status fill:#FFE4B5
    style Repos fill:#FFE4B5
```

---

## 4. 状态机流转图

```mermaid
stateDiagram-v2
    [*] --> pending: 创建Batch

    pending --> uploaded: CompleteUpload<br/>(所有文件上传完毕)
    uploaded --> scattering: Orchestrator<br/>消费BatchCreated

    scattering --> scattered: Redis Barrier<br/>count==totalFiles<br/>(所有文件解析完成)

    scattered --> gathering: 触发Gather<br/>(Python Worker)
    gathering --> gathered: 聚合完成<br/>(所有数据处理完毕)

    gathered --> diagnosing: 触发AI<br/>(AI Agent)
    diagnosing --> completed: 诊断完成

    completed --> pending: 复用<br/>(重新处理)

    scattering --> failed: 解析超时<br/>错误
    gathering --> failed: 聚合失败
    diagnosing --> failed: AI调用失败

    failed --> [*]: 终止

    note right of scattering
        当前状态
        需要修复：
        Worker查询TotalFiles
    end note

    note right of scattered
        待实现
        Python Worker
    end note

    note right of diagnosing
        待实现
        AI Agent (eino)
    end note
```

---

## 5. Kafka 事件流图

```mermaid
graph LR
    subgraph "事件发布者"
        Ingestor[Ingestor]
        Orchestrator[Orchestrator]
        Worker[Worker]
    end

    subgraph "Kafka Topic"
        Topic[batch-events<br/>Partition: 0]
    end

    subgraph "事件消费者"
        OrchestratorConsumer[Orchestrator<br/>orchestrator-group]
        WorkerConsumer[Worker<br/>cpp-worker-group]
        PythonConsumer[Python Worker<br/>python-group<br/>⬜]
        AIAgent[AI Agent<br/>ai-group<br/>⬜]
    end

    Ingestor -->|BatchCreated| Topic
    Orchestrator -->|StatusChanged| Topic
    Worker -->|FileParsed| Topic

    Topic -->|BatchCreated| OrchestratorConsumer
    Topic -->|BatchCreated| WorkerConsumer

    Topic -->|StatusChanged| OrchestratorConsumer

    Topic -->|FileParsed| OrchestratorConsumer
    Topic -->|FileParsed| PythonConsumer

    Topic -->|AllFilesGathered| AIAgent

    style Ingestor fill:#90EE90
    style Orchestrator fill:#90EE90
    style Worker fill:#90EE90
    style PythonConsumer fill:#FFB6C1
    style AIAgent fill:#FFB6C1
```

---

## 6. Redis 数据结构图

```mermaid
graph TB
    subgraph "Redis Keys"
        Barrier[batch:{id}:processed_files<br/>Type: Set<br/>TTL: 24h<br/>✅ 已实现]
        Cache[report:{id}<br/>Type: String<br/>TTL: 10m<br/>⬜ 待实现]
        Progress[batch:{id}:progress<br/>Type: Pub/Sub<br/>TTL: -<br/>⬜ 待实现]
    end

    subgraph "Barrier 操作"
        SADD[SADD fileID<br/>✅ 幂等添加]
        SCARD[SCARD<br/>✅ 获取计数]
        DEL[DEL<br/>✅ 清理]
    end

    subgraph "Cache 操作"
        GET[GET<br/>⬜ 查询缓存]
        SET[SET report 10m<br/>⬜ 写入缓存]
    end

    subgraph "Progress 操作"
        PUBLISH[PUBLISH progress<br/>⬜ 广播进度]
        SUBSCRIBE[SUBSCRIBE<br/>⬜ 订阅进度]
    end

    Barrier --> SADD
    Barrier --> SCARD
    Barrier --> DEL

    Cache --> GET
    Cache --> SET

    Progress --> PUBLISH
    Progress --> SUBSCRIBE

    style Barrier fill:#90EE90
    style Cache fill:#FFB6C1
    style Progress fill:#FFB6C1
```

---

## 7. 部署架构图

```mermaid
graph TB
    subgraph "Docker Host"
        subgraph "容器组"
            IngestorC[ingestor<br/>:8080<br/>✅]
            OrchestratorC[orchestrator<br/>✅]
            WorkerC[mock-cpp-worker<br/>✅]
            QueryC[query-service<br/>⬜]
        end

        subgraph "基础设施容器"
            PG[(postgres:5432<br/>✅)]
            RD[(redis:6379<br/>✅)]
            KF[kafka:9092<br/>✅]
            MN[minio:9000<br/>✅]
            ZK[zookeeper:2181<br/>✅]
        end

        subgraph "网络"
            Network[argus-network<br/>bridge]
        end
    end

    IngestorC -->|依赖| PG
    IngestorC -->|依赖| MN
    IngestorC -->|依赖| KF

    OrchestratorC -->|依赖| PG
    OrchestratorC -->|依赖| RD
    OrchestratorC -->|依赖| KF

    WorkerC -->|依赖| KF
    WorkerC -->|依赖| MN

    QueryC -->|依赖| PG
    QueryC -->|依赖| RD

    KF -->|依赖| ZK

    IngestorC -.->|Network| PG
    OrchestratorC -.->|Network| RD
    WorkerC -.->|Network| KF

    style IngestorC fill:#90EE90
    style OrchestratorC fill:#90EE90
    style WorkerC fill:#90EE90
    style QueryC fill:#FFB6C1
```

---

## 8. 并发查询防护图（Singleflight）

```mermaid
sequenceDiagram
    autonumber
    participant Client as 客户端 (100并发)
    participant Gin as Gin HTTP
    participant SF as Singleflight
    participant Redis as Redis Cache
    participant PG as PostgreSQL

    Note over Client,PG: 场景：100个并发查询同一报告

    par 100个并发请求
        Client->>Gin: GET /api/v1/batches/:id/report
    end

    Gin->>SF: Do(batchID, func())

    Note over SF: Singleflight合并请求
    SF->>Redis: GET report:{id}

    alt 缓存命中 (90%)
        Redis-->>SF: Report
        SF-->>Gin: Report (shared=true)
    else 缓存失效 (10%)
        SF->>PG: SELECT * FROM reports WHERE id=?
        Note over SF: 只有1次数据库查询！
        PG-->>SF: Report
        SF->>Redis: SET report:{id} (10m TTL)
        SF-->>Gin: Report (shared=false)
    end

    Gin-->>Client: 200 OK (Report)

    Note over Client,PG: 结果：100并发 → 1次DB查询<br/>缓存击穿已防护
```

---

## 9. 完整组件依赖图

```mermaid
graph TB
    subgraph "cmd层"
        IngestorCmd[cmd/ingestor/main.go<br/>✅]
        OrchCmd[cmd/orchestrator/main.go<br/>✅]
        WorkerCmd[cmd/mock-cpp-worker/main.go<br/>✅]
        QueryCmd[cmd/query-service/main.go<br/>⬜]
    end

    subgraph "application层"
        BatchSvc[batch_service.go<br/>✅]
        OrchSvc[orchestrate_service.go<br/>✅]
        QuerySvc[query_service.go<br/>⬜]
    end

    subgraph "domain层"
        BatchDomain[batch.go<br/>✅]
        StatusDomain[status.go<br/>✅]
        EventsDomain[events.go<br/>✅]
        RepoDomain[repository.go<br/>✅]
    end

    subgraph "infrastructure层"
        KafkaProd[kafka/producer.go<br/>✅]
        KafkaCons[kafka/consumer.go<br/>✅]
        RedisInfra[redis/client.go<br/>✅]
        PostgresRepo[postgres/repository.go<br/>✅]
        MinioInfra[minio/client.go<br/>✅]
    end

    IngestorCmd --> BatchSvc
    OrchCmd --> OrchSvc
    WorkerCmd --> KafkaCons
    QueryCmd --> QuerySvc

    BatchSvc --> BatchDomain
    OrchSvc --> BatchDomain
    QuerySvc --> BatchDomain

    BatchSvc --> RepoDomain
    OrchSvc --> RepoDomain
    QuerySvc --> RepoDomain

    BatchSvc --> KafkaProd
    OrchSvc --> KafkaProd
    OrchSvc --> KafkaCons
    OrchSvc --> RedisInfra
    QuerySvc --> RedisInfra

    RepoDomain -.-> PostgresRepo
    RepoDomain -.-> MinioInfra

    WorkerCmd --> KafkaProd
    WorkerCmd --> KafkaCons

    style IngestorCmd fill:#90EE90
    style OrchCmd fill:#90EE90
    style WorkerCmd fill:#90EE90
    style QueryCmd fill:#FFB6C1
```

---

## 10. 性能瓶颈与优化点

```mermaid
graph TB
    subgraph "已优化"
        Opt1[文件上传<br/>✅ 流式上传 MinIO<br/>避免OOM]
        Opt2[分布式Barrier<br/>✅ Redis Set<br/>幂等+计数]
        Opt3[事件解耦<br/>✅ Kafka异步<br/>水平扩展]
    end

    subgraph "待优化"
        Opt4[并发查询<br/>⬜ Singleflight<br/>防缓存击穿]
        Opt5[进度推送<br/>⬜ SSE + Redis Pub/Sub<br/>实时通知]
        Opt6[AI诊断<br/>⬜ Token成本控制<br/>Summary剪枝]
    end

    subgraph "监控点"
        Mon1[Kafka Consumer Lag<br/>⬜ 待实现]
        Mon2[Redis 内存使用<br/>⬜ 待实现]
        Mon3[API Response Time<br/>⬜ 待实现]
    end

    style Opt1 fill:#90EE90
    style Opt2 fill:#90EE90
    style Opt3 fill:#90EE90
    style Opt4 fill:#FFD700
    style Opt5 fill:#FFD700
    style Opt6 fill:#FFD700
```

---

**使用说明**：
- ✅ 绿色：已完成并验证
- 🟡 黄色：待实现（高优先级）
- 🟥 粉色：待实现（中优先级）
- ⬜ 灰色：未开始

**图表说明**：
1. 系统整体架构图 - 展示所有组件及其关系
2. 数据流向图 - 完整的业务流程时序图
3. DDD 分层架构图 - 展示依赖倒置原则
4. 状态机流转图 - Batch 的 8 个状态转换
5. Kafka 事件流图 - 事件发布与订阅关系
6. Redis 数据结构图 - Barrier、Cache、Pub/Sub
7. 部署架构图 - Docker 容器部署结构
8. 并发查询防护图 - Singleflight 防缓存击穿
9. 完整组件依赖图 - 代码级别的依赖关系
10. 性能瓶颈与优化点 - 已优化 vs 待优化
