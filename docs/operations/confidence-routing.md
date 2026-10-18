# 置信度分流

默认 `AI_RUNNER=hybrid` 使用以下路径。`conditional`/`supervisor` 是保留的旧执行器。

| 路径 | 门槛 | 模型调用 |
|---|---|---|
| L1 确定性规则 | 完整的日志发生次数统计；每条结构化日志都满足经代码审计的证据谓词 | 0 |
| L2 历史案例 | Top-1 > 0.88；前三条独立且经人工确认的案例根因一致；第三条 >= 0.72；异类差距 >= 0.05；模块、异常类型与当前事件一致 | 1 次总结 |
| 调查 Agent | 新问题、中间分数、证据冲突、检索失败、总结失败 | 受步数和总预算约束 |

规则表在应用启动时读取并驻留内存，无需每个请求访问 Redis。默认规则验证校验和不一致、实际耗时超过超时阈值、内核 OOM 标记且退出码 137。仅退出码 137 或通用 CAN 超时不能证明具体上游根因。规则命中仍输出 provisional（待确认）报告。

配置示例：

```bash
export AI_RUNNER=hybrid
export AI_HYBRID_THRESHOLDS='{"high":0.88,"low":0.72,"margin":0.05}'
# 可选：覆盖默认映射。仅允许 checksum/timeout/oom 三个代码谓词。
export AI_HYBRID_RULES='[{"id":"planner-oom-v1","error_code":"OOM_KILL","module":"planner","exception":"OOMKilled","predicate":"oom"}]'
```

L2 必须配置真实向量服务 `EMBEDDING_API_KEY`、`EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`，并与数据库既有向量采用同一模型与 1536 维空间。普通文本检索结果不能伪装成余弦相似度。

历史案例在独立确认根因后，由操作人员显式加入可信库（至少三个独立批次）：

```bash
go run ./workers/ai-agent/cmd/case-verify \
  -diagnosis-id '<已完成诊断的UUID>' \
  -cause 'planner-memory-leak' \
  -module 'planner' \
  -exception 'OOMKilled' \
  -reviewer '<确认人员标识>'
```

该命令使用 `DATABASE_URL` 和向量配置，原子更新向量及 `root_cause_id/case_verified/case_module/case_exception`，记录确认人员和时间。模型报告不会自动验证自己。未确认的旧案例会保守回退，直到人工完成标注。取消确认可由数据库管理员将该案例 `aggregated_data.case_verified` 置 false。

`routing` 随诊断落库并由报告查询接口返回：路径、规则 ID、分数、相似度、异类差距、案例 ID 和策略版本。分流分数不是根因正确概率；`Hit@5` 也不是置信度校准。差距按“首个不同根因”计算，避免同类案例互相挤占分数。没有异类时 margin=1 表示当前 Top-5 未发现竞争者，不代表全库没有竞争者。

当前证据边界保持保守：至多 100 条、16KB、单一错误码、完整覆盖。跨模块或复合错误进入 Agent。检索超时为 2 秒，调用方取消会停止整个诊断。总结失败回退时扣除已经预留的预算，并保留已报告的 token 消耗；服务端错误可能无法返回实际用量。错误摘要不会被直接复制为已证实根因。

验证：`TestConfidenceRoutingE2E` 使用实际 CSV 解析、真实 PostgreSQL/pgvector、案例确认入口、生产诊断服务、幂等落库和报告读取。向量与模型响应是可重复的受控夹具；它验证分流与调用次数，不证明模型诊断质量。完整流水线测试另覆盖 Kafka 与对象存储。

不承诺固定的 30%–40%/40%–50% 命中率或 <1ms/10–30ms 延迟；这些数字必须使用实际流量和部署环境测量。
