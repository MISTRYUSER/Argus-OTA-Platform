# Argus OTA Platform

面向车载软件版本回归的日志处理与证据调查平台：上传 → 实际解析 → Python 数值对比 → 固定取证或 Eino Agent 调查 → 可追溯报告。版本统计由代码完成，模型负责按需取证和解释；候选原因不等于已证明因果。

## 已接入能力

- **真实处理默认启用**：CSV 以及公开 Apollo Cyber Record；解析器与样例固定版本、许可和哈希。原项目私有 Inceptio `.rec` 与公开格式不能混用。原笔记本依赖的 `parser/pylib` 未公开，不声称已验证该私有格式。
- **版本回归报告**：明确指定基线/候选版本，按车辆、车型/硬件标签、模块、回放和工况匹配，检查窗口、采样密度和采样偏移。输出 CPU/内存/错误率变化、新错误、资源/版本/可选变更时间线、原始文件与行号、候选原因和待验证项。缺失指标保持未知。
- **证据分流**：默认 `AI_RUNNER=hybrid`。明确的校验值不一致或下载超时，且原始观测覆盖完整、无规则冲突，固定取证后只调用模型总结一次；其他输入进入原调查 Agent。禁止用模型自报置信度路由。预算不足返回 `resolution_status=unresolved`，保留已提交用量并要求人工复核。
- **跨实例额度**：AI Worker 默认启用 PostgreSQL 原子共享许可；每次实际调用/429 重试都要入场。额度不足或存储不可用延后任务，不消耗调查步骤。作用域需覆盖相同账号/供应商/模型，各副本的容量与超时配置必须一致。
- **可靠性沿用现有机制**：outbox、检查点、可续租任务所有权、执行代次、故障隔离与显式位点提交。解析产物、结果和完成事件幂等保存；不宣称外部模型调用或计费严格一次。

## 运行

Go 1.24.12、Python 3.12。先 `go mod download`、`make infra-up`、`make db-migrate`，已有数据库需要执行迁移 08、09。MinIO 的原 Docker Hub 镜像已停止分发，本地 compose 改为固定官方源码构建；首次构建需要联网。复制 `.env.example` 为忽略的 `.env.local`，通过私有文件配置模型密钥。支持的格式/上传例子见[处理链路说明](docs/benchmarks/version-regression-guide.md)。

```bash
python3 -m venv .venv
.venv/bin/pip install -r workers/python-aggregator/requirements-record.txt
# .env.local 中 ARGUS_PYTHON=.venv/bin/python
# 比较顺序必须显式配置；两项均为空时只做描述统计。
# ARGUS_BASELINE_VERSION=v1
# ARGUS_CANDIDATE_VERSION=v2
make run
```

Ingestor :8080；Query :8081；进度 SSE :8090。创建批次 `POST /api/v1/batches`，上传 `POST /api/v1/batches/:id/files`，完成 `POST /api/v1/batches/:id/complete`；报告与进度接口分别为 `/report`、`/progress`，事件流为 `/progress/stream`。录制文件的 multipart `metadata` 独立保存每个文件的版本和回放信息。CSV 自带这些列。

`ARGUS_PROCESSING_MODE=mock` 才运行旧模拟解析/聚合。`AI_RUNNER=investigation` 可强制全部走 Agent，`supervisor`、`conditional` 保留旧模式。可选独立 embedding 供应商与有范围的遥测工具沿用原配置。

## 回归与量化

```bash
scripts/regression.sh stage-name
python -m unittest discover -s workers/python-aggregator -v
python scripts/benchmark/version_regression.py --out /tmp/version-regression.json
go run ./workers/ai-agent/cmd/agent-bench -fixture -out /tmp/hybrid-fixture.json
```

设置 `ARGUS_TEST_DATABASE_URL`、`ARGUS_TEST_REDIS_ADDR`、`ARGUS_TEST_KAFKA_BROKERS`、`ARGUS_TEST_MINIO_ADDR` 运行真实基础设施测试；CI 自动提供这些服务，导出所有测试和测量。测试强制完整追踪采样，避免环境的低采样率破坏验证。

**诊断质量评测已经进行，但未证明 Agent 优于单次调用。** [2026-10-12 ITBench 结果](docs/benchmarks/results-2026-10-12.md)保留了低于基线的结果。[新增能力量化报告](docs/benchmarks/version-regression-results.md)区分公开录制解析、合成故障注入、真实多进程恢复和真实付费模型评测。模拟 HTTP 的调用节省不能写成真实费用下降或生产准确率。

## 边界

公开版处理入口每个文件与聚合输入上限 32 MiB、100000 条观测；二进制录制的版本和车辆标签由调用方明确提供，不从容器版本猜测。匹配标签不能证明环境完全一致，必须复跑/回滚验证因果。

共享许可保护客户端 HTTP 生命周期；断连后供应商仍可能继续计算，数据库会话丢失与不遵守超时的客户端组合也不在严格并发保证内。崩溃许可按请求上限与宽限期回收；流式模型接口尚未启用。任务故障后检查数据库失败状态与 DLQ，禁止直接自动重跑终态失败任务。

### OTA 故障现场回放评测

[受控故障包与三策略对照](docs/benchmarks/ota-fault-replay.md)：实际 HTTP 执行 12 场景，健康对照及预设恢复检查全部通过；固定流程命中 2/12。版本清单绑定真实实验 Git 对象与产物哈希。真实模型质量/费用对照等待供应商凭据，尚无收益结论。
