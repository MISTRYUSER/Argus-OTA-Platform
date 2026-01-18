# 🚀 Getting Started with Argus OTA Platform

本文档将指导你快速启动 Argus OTA Platform 的开发环境。

## 前置要求

确保你的系统已安装以下工具：

- Docker Desktop (推荐最新版本)
- Docker Compose
- Go 1.21+
- Make (可选，用于快速命令)
- Python 3.9+ (用于 AI Agent Worker)

## 📋 快速开始

### 1. 启动基础设施

使用 Makefile（推荐）：

```bash
make quick-start
```

或者手动启动：

```bash
cd deployments
docker-compose up -d
```

这将启动以下服务：
- PostgreSQL (端口 5432)
- Redis (端口 6379)
- Kafka (端口 9092)
- MinIO (端口 9000, 控制台 9001)
- Kafka UI (端口 8080)
- Redis Commander (端口 8081)
- PgAdmin (端口 5050)

### 2. 验证服务状态

```bash
# 查看运行中的容器
make infra-ps

# 或使用 docker-compose
docker-compose ps

# 查看服务日志
make infra-logs
```

### 3. 访问管理界面

访问以下 URL 来管理各个服务：

| 服务 | URL | 凭据 |
|------|-----|------|
| Kafka UI | http://localhost:8080 | - |
| Redis Commander | http://localhost:8081 | - |
| MinIO Console | http://localhost:9001 | minioadmin/minioadmin |
| PgAdmin | http://localhost:5050 | admin@argus.com/admin |

### 4. 连接数据库

```bash
# 使用 psql 连接
make db-connect

# 或使用 docker exec
docker exec -it argus-postgres psql -U argus -d argus_ota
```

### 5. 初始化 Go 模块

```bash
# 初始化 Go 模块
go mod init github.com/yourusername/argus-ota-platform

# 安装依赖
make deps
```

## 🔧 常用命令

### 基础设施管理

```bash
# 启动所有基础设施
make infra-up

# 停止所有基础设施
make infra-down

# 重启基础设施
make infra-restart

# 查看日志
make infra-logs

# 清理所有 Docker 资源
make docker-clean
```

### 数据库操作

```bash
# 连接数据库
make db-connect

# 导出数据库结构
make db-dump

# 重置数据库（⚠️ 警告：会删除所有数据）
make db-reset
```

### 开发命令

```bash
# 构建所有服务
make build

# 运行所有服务
make run

# 运行单个服务
make run-ingestor
make run-orchestrator
make run-query

# 运行测试
make test

# 运行测试并生成覆盖率报告
make test-coverage

# 代码格式化
make fmt

# 代码检查
make lint
```

## 📁 项目结构

```
argus-ota-platform/
├── cmd/                    # 服务入口
│   ├── ingestor/          # Gin 接入服务
│   ├── orchestrator/      # DDD 编排层
│   └── query-service/     # 报告查询服务
│
├── internal/              # 核心业务逻辑
│   ├── domain/           # 领域模型
│   ├── application/      # 用例层
│   ├── infrastructure/   # 技术实现
│   └── interfaces/       # HTTP/SSE 接口
│
├── workers/              # 多语言 Workers
│   ├── cpp-parser/       # C++ 解析器
│   ├── python-aggregator/# Python 聚合器
│   └── ai-agent/         # AI 诊断 Agent
│
├── deployments/          # 部署配置
│   ├── docker-compose.yml
│   ├── init-scripts/     # 数据库初始化脚本
│   └── env/              # 环境变量
│
└── Makefile             # 快速命令
```

## 🔐 环境变量配置

复制环境变量模板并修改：

```bash
cp deployments/env/.env.example deployments/env/.env
```

然后编辑 `.env` 文件，配置你的环境变量。

## 📊 数据库 Schema

数据库表将在第一次启动时自动创建。初始化脚本位于：

```
deployments/init-scripts/01-init-schema.sql
```

主要表结构：
- `batches` - 批量任务表
- `files` - 文件信息表
- `ai_diagnoses` - AI 诊断结果表
- `reports` - 报告缓存表

## 🧪 测试连接

### 测试 PostgreSQL

```bash
docker exec -it argus-postgres psql -U argus -d argus_ota -c "SELECT version();"
```

### 测试 Redis

```bash
docker exec -it argus-redis redis-cli ping
# 应该返回 PONG
```

### 测试 Kafka

```bash
docker exec -it argus-kafka kafka-topics --bootstrap-server localhost:9092 --list
```

### 测试 MinIO

访问 MinIO 控制台：http://localhost:9001

## 🐛 故障排除

### 端口冲突

如果某些端口已被占用，修改 `deployments/docker-compose.yml` 中的端口映射。

### 服务无法启动

```bash
# 查看服务日志
docker-compose logs <service-name>

# 重启服务
docker-compose restart <service-name>
```

### 清理并重新开始

```bash
# 停止所有服务
make infra-down

# 清理所有数据（⚠️ 警告：会删除所有数据）
make docker-clean

# 重新启动
make quick-start
```

## 📚 下一步

1. 阅读 [架构文档](./architecture/overview.md)
2. 查看 [Schema 设计](./schemas/postgres.md)
3. 开始开发你的第一个服务

## 🔗 相关链接

- [Gin 文档](https://gin-gonic.com/docs/)
- [Redis 文档](https://redis.io/docs/)
- [Kafka 文档](https://kafka.apache.org/documentation/)
- [MinIO 文档](https://min.io/docs/minio/linux/index.html)
- [PostgreSQL 文档](https://www.postgresql.org/docs/)
