# 诊断质量与混合路由收益：2026-10-15 核查

核查基线：PR #12，`5b382110273881bbe761974b54e757335866eafd`。生产 `hybrid.Eligible`、阈值和提示词保持原样。

## 两条实验线

| 实验 | 输入与执行器 | 可以回答 | 不能回答 |
|---|---|---|---|
| SRE 诊断迁移质量 | ITBench-Lite 原盲适配输入，现有 agent-bench 与官方评审/实体评分 | 在省略应用日志、指标、链路后的离线现场中识别根因的表现 | OTA 质量、生产泛化、SRE 固定分支节省 |
| OTA 质量及调用收益 | 现有实际 HTTP 故障包，三策略，score_ota_faults | 同现场中三策略质量、未决、调用、用量、费用和延迟差异 | 生产业务收益、Agent 自动修复、全新故障类别泛化 |

当前 SRE 只有 id/lines/sha256，所有场景退回调查。即便补齐统计及覆盖字段，生产 Eligible 也只接受 OTA_CHECKSUM_MISMATCH/OTA_DOWNLOAD_TIMEOUT：Kubernetes reason 不属于这两个类型。字段缺失与故障语义不匹配是两个独立障碍。不要制造 OTA 错误码或 complete=true 来跨过它们。

SRE 原基线仅接收 24000 字符前缀，Agent 可以选择性检索同一快照的其他部分。因此它是架构比较，不是等量实际证据的单变量实验；保留原质量结果及其限制，不与 OTA 调用收益合并。若修正适配器后输入摘要改变，需三策略全部重跑，禁止与旧结果混算。

## 复用 OTA 的完整验证

1. 原生成器重跑 12 个实际故障，检查注入、健康对照和预设恢复；保留 receipt、公开输入、私有真值、版本/产物哈希。不重建故障平台。
2. 对 all/dev/heldout 分别执行现有 `-preflight-only`，预期为 2/12、1/6、1/6 固定分支。以本次 CLI 回执为准；失败时调查输入和生成过程，不能改阈值追求命中率。
3. 本地脚本化 HTTP 只测执行链路、报告失败、费用未知传播。现有 CLI 测试覆盖固定路径；新增同一测试中的非覆盖输入场景，验证 hybrid 确实进入调查并发起工具调用。脚本给出的调用差不能当真实模型收益。
4. 开发集用于调试后冻结协议；留出集 6 场景各 3 次、三策略共 54 个预定任务，失败/未决/缺文件全部纳入分母。all 的 108 个任务只能作全包描述，不能替代留出质量结论。留出仍是同机制不同参数。
5. 总体结果与固定/调查两层同时报告。评分器新增 `summary_by_hybrid_route`，按同一 case/trial 的 hybrid 回执路径配对 baseline/hybrid/agent。每层复用原质量、证据、失败、调用、费用及 P95 口径；缺 hybrid 回执的配对进入 unknown，三策略全部预定任务仍保留，不按 baseline/agent 自己的路径分组。

这里的 actual_route 由执行器按生产 Eligible 记录，表示所选路径；若建模客户端初始化失败或预算不足，不能据此声称模型已执行该路径。是否发出请求仍看 actual_http_calls/request_tool_policy。分层不新增“成本节省”结论，也不推断缺失回执的路径。

调用收益应同时报告 hybrid 对全 Agent、hybrid 对单次总结基线的配对差值，保留负收益；固定分支仍需一次总结，不能称为零模型调用。质量及未决率先达预先声明的容忍边界，再讨论调用收益；样本很小，应给逐 case/trial 结果，不凭总体均值宣称稳健提升。空层无质量均值，recorded_http_calls=0 仅表示无已记录调用。

真实模型对照必须绑定同一请求模型/端点、可核查供应商返回版本、温度/输出上限、任务超时、重试、步数、预算、上下文上限、完整输入摘要、套件版本及价格表。输入相同指可访问现场一致，不要求 Agent 与固定流程发出相同消息。现有评分器检查模型/端点/步数/预算/上下文/价格、输入及版本；温度/输出上限/180 秒超时来自同版执行器，故必须使用同一执行器提交。供应商只返回别名时如实标记不可验证权重版本。

价格表使用同一日期、来源、币种及缓存计费口径；跨全部 HTTP 尝试统计用量，失败重试不得删掉。缺用量或价格保留 null，不以零补齐。顺序运行可能有缓存与供应商负载偏差：保存缓存命中并交错/随机化三策略执行顺序，报告运行时间；不能把单次延迟差当分流收益。

## SRE 字段能否从真实数据可靠构建

以下是对下载器及适配代码的核查，本轮没有重新下载 ITBench 原始数据，不能据此报告原始文件字段完整率。

| 信息 | 真实来源与可构建范围 | 禁止推断 |
|---|---|---|
| 事件统计 | 原始 Event UID、API 版本、对象 UID、reason、type、时间、count/series；先按事件身份取最新观测，再统计 | TSV 行数、350 字符块数不是故障发生次数；累计 count 不能跨采样累加 |
| 容器异常状态 | Pod status.containerStatuses/initContainerStatuses：waiting、terminated、lastState、restartCount | CrashLoopBackOff 是重启退避症状，不能直接推为配置错误或根因 |
| 调度/探针等观测 | Pod conditions 与关联 Warning Event，保留对象/容器/节点关联 | 单一 reason 不证明唯一原因；跨对象冲突不得丢弃 |
| 告警 | alert 名称、labels、状态、观测时间及文件指针 | 告警条数不等于日志/错误次数；没有告警不等于健康 |
| 覆盖摘要 | 文件清单/哈希、原始行数、成功解析/失败/排除/去重数、保留记录及分块映射 | 目前筛 Warning、限 otel-demo、保留最新快照且省略三种模态，不能声称全系统 complete |

原适配器只保留 involvedObject，没有处理 events.k8s.io/v1 的 regarding/note；事件 UID/count/series 也没有进入输出。拟建契约必须从原文件构建，不能从现有 lines 逆推出丢失字段。本次最小修复仅让现有 Warning 去重按 TimestampTime 保留最新观测，与原对象快照选择约定一致；时间字符串继续沿用现有数据格式约定，不扩充 API/命名空间/模态或路由。

## 独立 SRE 契约提案（尚未实现或接入生产）

使用 `sre_observation_schema=k8s-v1`，与 OTA `hybrid_evidence_schema=v1` 分离：

- `scope`：namespace/cluster 标识、观测窗口、各模态可用/缺失状态；无法获得窗口则明确 unknown。
- `sources`：每个原文件哈希、行号/JSON 指针、采集时间、适配器版本；完整输入摘要同时绑定 observations、statistics、coverage、sources。
- `observations`：结构化事件、Pod/容器状态与告警。保留 Event apiVersion/UID、对象 UID/kind/namespace/name、容器、reason/type/note、首次/末次发生时间、采集时间、累计 count 及原字段。core/v1 与 events.k8s.io/v1 分别规范化。
- `statistics`：分别记录 `unique_event`、`event_series_cumulative_count`、`object_snapshot`、`container_state`、`alert_snapshot`，按实体与时间作用域分组。缺 UID/有效计数时记录 unknown 或低可信去重，禁止假装精确发生次数。
- `coverage`：每源 raw/parsed/malformed/filtered/retained/deduplicated 计数、过滤规则、截断/缺失及逻辑记录到 log:N 的分块映射。`complete_within_declared_scope` 仅在清单穷尽、零解析损失、零截断且映射可核验时为 true，仍不等于现场所有模态完整。存在未处理 delete/过期状态时不能把快照当当前全量状态。

候选 SRE 固定取证必须独立设计并审计，例如只陈述“此容器当前等待拉取镜像，相关事件报告鉴权失败”，不把 ImagePullBackOff 当唯一根因。缺关联 UID、事件过旧、多容器/多实体冲突、解析损失或缺模态，一律保留调查。若未来获得批准，新增独立 SRE eligibility/policy 及版本，而不是放松 OTA Eligible。本次不实现新生产规则。

## 独立验证顺序

1. 标签盲的双解析器：一个直接扫描原 TSV/JSON，另一个适配；逐条核对身份、时间、计数、来源映射和摘要。更改 ground_truth.yaml 不得改变公开输入。原始文件下载及校验成功后才出真实字段覆盖报告。
2. 针对原始数据样式的回归：乱序、重复采样、同名 Pod 重建/不同 UID、core/v1 与 events.k8s.io/v1、事件累计计数、缺字段、损坏 JSON、过期/删除、多对象冲突、长记录分块；不把片段当事件。
3. 冻结独立 SRE 取证策略及拒绝条件，再用预先划定的开发/留出原始场景做无模型覆盖预检；以完整 suite 计分母，记录每例接受/拒绝原因。零固定覆盖是有效结果，不以标签反调路由。
4. 专家独立复核可固定场景的证据充分性及错误接受率；通过后才复用 agent-bench 增加独立 SRE policy 适配，运行同协议三策略真实模型实验。质量比较、分流覆盖、调用、计费四类回执分开保存。

官方语义来源：
- https://kubernetes.io/docs/reference/kubernetes-api/core/event-v1/ ：core Event 的 count、involvedObject 和时间字段。
- https://kubernetes.io/docs/reference/kubernetes-api/events/event-v1/ ：regarding、note、series.count/lastObservedTime。
- https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/ ：容器状态与 CrashLoopBackOff 的症状含义。

## 本次验证回执

[机器回执](evaluation-contract-audit-2026-10-15-receipt.json) 来自本轮真实 HTTP 故障生成及生产 CLI 预检：注入/健康/预设恢复均为 12/12；all 固定/调查为 2/10，dev 和 heldout 均为 1/5。回执绑定新生成输入文件哈希与套件摘要，没有真实模型调用。SRE 原始文件未重新下载，原 0/10 schema 预检未冒充新运行。

Python benchmark 全套 65 项通过；Go `go test ./...` 在 `OTEL_TRACES_SAMPLER=always_on` 下退出 0，外部基础设施/付费测试仍按原配置跳过。首次 Python 全套因缺 pytrec_eval 有五项报错，安装仓库 CI 固定版本 pytrec-eval-terrier==0.5.10 后重跑通过。首次未指定全采样的 Go 运行在已有 TestFailedBenchmarkKeepsTraceWithoutDiagnosis 越界；使用全采样后通过，未修改该项 trace 测试或生产采样配置。

`go test -race -count=1` 对 agent-bench 与 hybrid 两包通过。生产路由文件无差异；新增 Go 回归证明补齐统计/覆盖的 Kubernetes reason 仍不能进入 OTA 固定路径。
