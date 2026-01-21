Argus OTA Platform - 开发进度追踪

更新时间: 2025-01-18 (v2.0)
总体进度: 20% ▰▱▱▱▱▱▱▱▱▱
当前阶段: Ingestor（接入层）完成 ✅ -> 转向 Infra 搭建

1. 快速概览

核心服务状态

服务

状态

完成度

说明

Ingestor

✅ 完成

100%

HTTP API + MinIO 流式上传

Orchestrator

⬜ 未开始

0%

核心调度器 (Kafka Consumer)

C++ Worker

⬜ 未开始

0%

采用 Mock 策略 (模拟解析)

Python Worker

⬜ 未开始

0%

采用 Mock 策略 (模拟聚合)

AI Worker

📝 设计完成

10%

Eino 架构设计完成，SSE 免开发

Query Service

⬜ 未开始

0%

暂缓，合并至 Ingestor

2. 模块进度详情 (已裁剪低优任务)

2.1 Domain 层（70% 🟡）

核心逻辑已稳固，暂无变更。

[x] 聚合根 (Batch, File)

[x] 状态机 & 领域事件

[x] 仓储接口定义

2.2 Infrastructure 层（40% 🟡）

Day 6 的绝对重点

✅ 已完成

[x] Postgres & MinIO & Kafka Repository/Client 基础封装

⬜ 待完成 (Day 6 必做)

[ ] Docker Compose 环境 (一键拉起 PG, MinIO, Kafka, Redis)

[ ] SQL Migration (建表：batches, files, ai_diagnoses)

[ ] Redis Client (用于简单的计数器 Barrier)

[ ] Kafka Consumer Group (Orchestrator 的心脏)

2.3 Application 层（50% 🟡）

✅ 已完成

[x] BatchService (上传逻辑)

[x] KafkaPublisher

⬜ 待完成

[ ] OrchestratorService

[ ] 监听 Kafka BatchCreated

[ ] 驱动状态机

[ ] 发布 FileProcessingStarted

[ ] AI Service (Application)

[ ] 调用 Eino Stream 接口

[ ] 简单的 Prompt 组装

2.4 Interfaces 层（50% 🟡）

✅ 已完成

[x] BatchHandler (HTTP API)

⬜ 待完成

[ ] ResultHandler

[ ] GET /batches/:id (查询进度，简单的轮询接口)

[ ] GET /batches/:id/diagnosis (直接透传 Eino Stream)

[x] ~~SSE Handler (手写连接管理)~~ -> 已移除 (Eino 接管)

2.5 Workers (策略变更: Mock First)

⬜ 待完成

[ ] C++ Worker (Mock 版)

[ ] 纯 Go 实现，模拟消费 Kafka

[ ] time.Sleep(1s) 模拟解析

[ ] 随机生成 0x8004 错误码写入 DB

[ ] Python Worker (Mock 版)

[ ] 纯 Go 实现，模拟聚合

[ ] 更新 Batch 状态为 Finished

[ ] AI Worker (Eino 版)

[ ] 集成 Eino SDK

[ ] 连接 DeepSeek/OpenAI API

[ ] RAG (pgvector) 简单查询

3. 调整后的下一步计划 (10天冲刺表)

🚀 阶段一：让系统跑起来 (Day 6-7)

目标：Docker 启动，数据库跑通，上传接口真实可用。

[ ] Day 6 (Infrastructure Day)

[ ] 编写 docker-compose.yml (PG+Vector, Redis, Kafka, MinIO)

[ ] 编写 init.sql 并执行建表

[ ] 本地启动 Ingestor，测试真实上传文件到 MinIO，记录写入 PG。

[ ] Day 7 (Linkage Day)

[ ] 实现 Kafka Consumer (Orchestrator 基础)

[ ] 验证：Ingestor 发消息 -> Kafka -> Orchestrator 收消息。

⚙️ 阶段二：调度与 Mock Worker (Day 8-10)

目标：整个状态机流程跑通，数据库状态会变。

[ ] Day 8 (Orchestrator Core)

[ ] 实现状态机驱动逻辑 (Created -> Processing)

[ ] Redis 简单计数器 (Barrier)

[ ] Day 9 (Mock Workers)

[ ] 写一个 Go 程序 cmd/mock_worker/main.go

[ ] 模拟 C++ 解析 (随机写个 ErrorCode 进库)

[ ] 模拟 Python 聚合 (改 Batch 状态为 Done)

[ ] Day 10 (End-to-End Test)

[ ] 联调：上传 -> 自动流转 -> 数据库显示“已完成”。

🧠 阶段三：注入灵魂 (Day 11-13)

目标：AI 介入，生成诊断报告。

[ ] Day 11 (RAG Setup)

[ ] 在 PG 中手动插几条向量数据 (Mock 知识库)

[ ] 实现简单的 SQL: SELECT ... ORDER BY embedding <-> query

[ ] Day 12 (Eino Integration)

[ ] 接入 Eino，串联 RAG + LLM

[ ] 实现 GET /diagnosis 接口，透传 Eino Stream

[ ] Day 13 (Demo Polish)

[ ] 录制演示视频

[ ] 整理代码库和文档

4. 技术债务 (已精简)

[ ] 日志: 暂时只用 log.Println，暂不引入 Zap。

[ ] 配置: 环境变量读取目前够用了。

[ ] 测试: 暂缓单元测试，优先保端到端(E2E)流程通畅。

5. 里程碑更新

[x] M1: Ingestor & Domain (Day 1-5) - ✅ 完成

[ ] M2: Infra & Docker (Day 6) - 🔥 明日重点

[ ] M3: 流程跑通 (Mock) (Day 10) - 📅 目标

[ ] M4: AI & RAG (Day 13) - 📅 目标