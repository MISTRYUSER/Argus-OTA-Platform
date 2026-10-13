# 实际处理与复现

## 公开录制文件与解析器

配对来源：[cyber-record](https://github.com/daohu527/cyber-record)，固定提交 `f0b4be600dfd6fc9aac3db898a93337a091a324c`，Apache-2.0。样例保存于 `testdata/public-record/example.record.00000`。SHA-256：`3d88a00681de8bdd077430bf3adb883f9ab94799b573f6860492b701dcfd777b`。

```bash
python3 -m venv .venv
.venv/bin/pip install -r workers/python-aggregator/requirements-record.txt
.venv/bin/python scripts/benchmark/public_record.py \
  --metadata '{"version":"public-fixture-unmeasured","vehicle_id":"public-sample","vehicle_class":"unknown","replay_id":"apollo-example","workload":"public-simcontrol"}' \
  --out /tmp/public-record.json
```

样例实际包含 34 条消息，5 个有消息的主题。没有 CPU/内存/监控错误观测；错误率保持未知。示例 `version` 是调用方标签，不是从记录里测得的 OTA 软件版本。

## 上传二进制录制

```bash
curl -X POST http://localhost:8080/api/v1/batches \
  -H 'Content-Type: application/json' \
  -d '{"vehicle_id":"public-sample","vin":"PUBLIC","expected_workers":1}'
# 将返回的 batch_id 填入以下路径。
curl -X POST http://localhost:8080/api/v1/batches/BATCH_ID/files \
  -F 'file=@testdata/public-record/example.record.00000' \
  -F 'metadata={"record_format":"apollo_cyber","version":"v1","vehicle_id":"public-sample","vehicle_class":"unknown","replay_id":"R1","workload":"simcontrol"}'
curl -X POST http://localhost:8080/api/v1/batches/BATCH_ID/complete
```

每个文件分别保存 `metadata`。若对同一公开样例加 v1/v2 标签重复上传，只能验证版本信息传递；因为没有真实两版程序运行和资源指标，不能证明 OTA 版本回归。

## CSV 对比契约

必需列：`timestamp,version,vehicle_id,vehicle_class,module,replay_id,workload,error_code,cpu,memory_mb`。

- 时间为带时区的 ISO 时间或非负 epoch 秒；同一时间戳的不同消息保留，重复消息身份被拒绝。
- CPU 沿用原笔记本每进程 `cpu_util` 单位，多核累计可以超过 100；内存单位 MiB。
- 空 CPU/内存表示未知；声明的 CSV 采样中空错误码表示无错误，二进制来源无错误信号时不能当作无故障。
- 比较顺序由 `ARGUS_BASELINE_VERSION`、`ARGUS_CANDIDATE_VERSION` 显式指定；均为空则只做描述统计。
- 每组每版至少 10 条观测，窗口/数量比例不超过 1.1，回放采样偏移不超过基线典型采样间隔 1%（最小 1 微秒）。阈值是公开版明确的可比性守门规则，没有通过生产校准。
- 可选 `change_id/change_description` 进入同一时间线；候选窗口起点只标为声明的版本边界，不冒充真实部署时间。
- 结果位于批次 `/report` 的 `version_regression`，含样本分母、窗口、统计、限制、原始文件/行号和验证建议；Agent 可以调用 `read_version_regression`。

## 固定流程条件

`testdata/csv/ota_checksum.csv` 是明确标注的人工观测示例。上传时保留 `error_type/expected_checksum/actual_checksum`；超时观测使用 `elapsed_ms/timeout_ms`。真实解析产物按错误出现次数统计，仓储验证完整日志覆盖后才给出覆盖契约。

固定规则只接受 `OTA_CHECKSUM_MISMATCH` 或 `OTA_DOWNLOAD_TIMEOUT`。缺字段、不同类型混杂、统计不一致、证据过大、校验值一致或超时时间未超过阈值，均回退调查。没有根据错误码直接猜“网络故障”等上游原因。

预算耗尽报告 `resolution_status=unresolved`，要求人工复核；报告生成完毕并不表示根因已确认。模型自报置信度不参与分流。

## 模型质量与费用评测

无密钥的 `-fixture` 只测试编排与引用校验。真实评测需要指定已有私有 DeepSeek 密钥文件，使用同一预划分留出集、相同模型和独立真值评分；失败/未决任务保留。

```bash
go run ./workers/ai-agent/cmd/agent-bench -mode agent -heldout \
  -input /path/blind-inputs.json -out /path/new-all-agent-results -trials 1
go run ./workers/ai-agent/cmd/agent-bench -mode hybrid -heldout \
  -input /path/blind-inputs.json -out /path/new-hybrid-results -trials 1
```

输入场景需明确 `partition=heldout`；评分器在预测完成后才读真值。`summary.json` 汇总全部任务、实际 HTTP 调用、供应商用量、平均耗时和 P95；未提供可审计计价信息时费用为 null，不能用 token 预留预算替代真实账单。

## 多实例

迁移 09 建立共享作用域与许可。两个 AI Worker 使用同一数据库和相同 `LLM_QUOTA_SCOPE`、`LLM_CONCURRENCY`、请求上限、宽限期。作用域需包含实际共享额度的账号，避免不同账号无意互相阻塞或同账号拆成不同作用域。

```bash
ARGUS_TEST_DATABASE_URL=postgres://... \
ARGUS_LOAD_REPORT_DIR=/tmp/measurements \
go test -count=1 -v ./workers/ai-agent/internal/infrastructure/postgres \
  -run '^TestSharedQuotaMultiprocessCrashRecovery$'
```

该测试使用独立 OS 进程、真实 PostgreSQL、SIGKILL、重复投递、429、30 秒任务租约和持久化检查点/outbox；模型供应商是本地 HTTP 服务。正常请求持有数据库会话锁直至返回，崩溃许可在请求上限与宽限期后回收。恢复 P95 的样本数只有 2，不作为生产 SLA。
