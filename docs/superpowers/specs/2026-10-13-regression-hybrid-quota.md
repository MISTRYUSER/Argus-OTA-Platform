# 版本回归诊断、证据分流与共享额度

用户要求直接完成三项能力并回归测试，交付可重现、可量化成果。

## 设计
- 原有事件与 outbox 协议不改。真实 parser/aggregator 以可配置入口接入上传文件、解析产物和报告存储；mock 仅显式选择时启用。
- 公开输入 CSV 包含 timestamp, version, vehicle_id, vehicle_class, module, replay_id, workload, error_code, cpu, memory_mb。严格校验、限制输入大小。原始文件与行号保留在证据内。rec 沿用笔记本 GeneralParse 调用，不重写二进制解析器；缺少私有依赖返回错误。
- Python 代码完成数值统计；按相同 replay_id/workload/车辆/模块匹配版本，窗口与样本量必须明确。不匹配工况只报告不可比，不下版本根因结论。报告包含变化、时间线、候选原因、证据和待验证项。
- hybrid runner 用明确错误类型、证据覆盖、规则冲突选择固定取证+一次总结或现有 investigation。禁止用模型自报置信度路由。预算不足输出未决报告与人工复核；拒绝伪造引用。
- 共享模型额度使用 PostgreSQL/Redis 原子租约，跨实例生效。调用前入场，每次实际重试均计数。容量耗尽走 DeferredTaskError；失效时关闭入场。并发许可必须覆盖真实 HTTP 生命周期，崩溃许可有界回收。

## 验收
- CSV→解析→聚合→版本报告端到端；同回放回归、不同工况、零基线、缺失指标、非法数值、输入版本混杂与重复消息。
- hybrid 与 all-agent 相同测试场景，记录全部成功/失败/未决、根因质量、实际调用、token、费用与 P95。模拟 HTTP 与真实付费模型结果分开，不以模拟成本证明真实质量。
- 两个以上独立进程共享持久存储，SIGKILL、重复输入、429 与接管验证；任务丢失、重复最终结果、接管耗时与跨实例峰值调用分别记录。
- 完整 go test、vet、build 与 Python 测试，集成测试跳过项单独披露。
