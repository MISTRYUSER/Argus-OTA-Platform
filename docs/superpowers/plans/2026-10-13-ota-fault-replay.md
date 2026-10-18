# OTA Fault Replay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans；本轮内联执行，结束时独立审阅。

**Goal:** 复用现有入口，用真正运行的 OTA 故障检验诊断和成本。
**Architecture:** Python 生成公开现场与独立真值，Go 复用生产执行器，Python 对全部计划任务评分。版本清单绑定真实 Git 对象及运行产物。
**Tech Stack:** Python 标准库 HTTP/Git、Go/Eino、现有 DeepSeek 兼容接口。
**Spec:** docs/superpowers/specs/2026-10-13-ota-fault-replay-design.md

## Global Constraints
- 12 场景；6 开发/6 留出；仅受控实验。
- 上下文 24000 字符，预算 1000000，12 步，180 秒；三模式一致。
- 不改 Eligible，不新增 Agent，不接生产修复。
- 无价格或用量费用 null；所有计划任务纳入分母。

## Review Focus
- private 标签泄露到公开适配或提示词：篡改 private 不改变 blind suite。
- 固定路径全未命中导致虚假比较：预检独立统计生产 Eligible。
- 超时的后台请求尚未结束：等待服务端线程收敛再保存现场。
- 缺失结果或 429 隐藏费用：按完整任务计划补失败；逐 HTTP 响应记用量。
- 最近提交误归因：加入无关变更，主因代码严格评分。

### Task 1: 实际故障与盲测包
**Files:** scripts/benchmark/ota_fault_lab.py, ota_fault_pack.py, test_ota_fault_pack.py
**Interfaces:** generate_pack(out: Path) -> dict；prepare_suite(public: Path, partition: str) -> dict。
- [ ] 写六类真实异常、健康/恢复运行、版本对象验证、真值隔离测试，运行确认缺实现失败。
- [ ] 实现标准库 HTTP/代理、请求采集、SHA/真实 Git 清单、公开适配。
- [ ] 运行新 Python 测试，归档实际 12 场景结果。

### Task 2: 现有三策略入口与费用
**Files:** workers/ai-agent/cmd/agent-bench/{main.go,ota.go,ota_test.go}, hybrid/runner.go
**Interfaces:** preflightSuite(suite)；OTA task instructions；HTTP trace usage receipts；-preflight-only, -model, -base-url, -pricing。
- [ ] 写场景 SHA/OTA 契约、三模式证据、路由预检、费用缺失/缓存/重试测试，观察失败。
- [ ] 接入 OTA 任务说明及确定取证 baseline；hybrid 只增加可选任务说明。
- [ ] 运行 Go 入口/混合分支测试，输出 12 场景覆盖。

### Task 3: 真值评分、交付与回归
**Files:** scripts/benchmark/{score_ota_faults.py,test_score_ota_faults.py}, docs/benchmarks/ota-fault-replay.md, workflow
**Interfaces:** score_suite(public_suite, private_labels, runs) -> dict。
- [ ] 写完整分母、错误归因/未决、证据获取 vs 引用、费用未知、输入不匹配拒绝测试；确认缺实现失败。
- [ ] 实现三模式评分与回归明细，文档写实际结果及真实模型 pending。
- [ ] 执行 Go/Python 全回归、vet/build；独立审阅，修正重要问题；创建分支 PR，不自动合并。

## 执行记录
- 设计采用用户提供的 A→B→C 顺序；本轮范围已明确，不再请求重复授权。
- main 基线 847c4a4；工作分支 codex/ota-fault-replay。
- 初次基线 trace_test 在默认采样下失败；设置仓库回归要求 OTEL_TRACES_SAMPLER=always_on 后通过。
- DeepSeek 环境/文件密钥未发现；沿用供应商，不创建 OpenAI 密钥。

- Task 1: complete；真实 12 场景、Git bundle、盲输入/真值隔离通过。
- Task 2: complete；三策略 HTTP 执行与费用回执通过；真实模型比较仍 blocked（没有密钥）。
- Task 3: 独立 reviewer 无 Critical，4 Important 均以重现测试修复：完整输入摘要、失败报告证据、检索与送入区分、首次请求锚定及生成失败关闭。新增压缩工具组集成检查。费用字段缺失/价格表差异属于可导致错误收益结论的问题，按 Important 处理并修复；HTTP body 耗时与 SRE 模型名称一并修正。
- Ruling: 留出仅是已知机制的参数变体，分类分数不能证明完整解释正确；文档明确要求真实运行后的语义审阅。若忽略此限制，会夸大泛化与因果正确性。
- Ruling: 缺供应商凭据，不伪造实测、不创建新账号或更换模型；仍完成全部可复现链路和工程回归。成本是 A 的真实质量/费用结果尚未交付。

- Ruling: 自动审批拒绝公开独立真值；真值保留在本地生成结果，公开仅保存绑定摘要，CI 产物排除 private。实验 Git 包已核查仅含 24 次 toy 仓库提交及两个实验文件，所有者目标验证后上传获准。
