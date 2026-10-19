# OTA 故障现场回放评测

本轮在 main `847c4a4` 上实现 A：**用实际执行产生的故障现场，复用生产调查执行器做三策略对照**；同时准备 B 的 Git/配置/产物来源清单。未增加分流规则或 Agent，未实现生产修复或 Agent 回放验证器。

## 本次实际结果

| 检查 | 实际结果 | 含义 |
|---|---:|---|
| 本地 HTTP 故障场景 | 12 | 六类原因，各两个参数变体 |
| 实际注入产生运行失败 | 12/12 | 确实运行后发生异常，不是手改 CSV |
| 健康对照成功 | 12/12 | 同输入正常配置可完成请求 |
| 预设恢复配置检查通过 | 12/12 | 由实验程序恢复，不是 Agent 找到并修复问题 |
| 生产混合策略固定分支命中 | 2/12 | 开发/留出各 1/6；其余 10 个走调查 |
| 真实付费模型根因/费用对照 | **未运行** | 当前没有可用 DeepSeek 私钥，不能写质量或费用收益 |

保存的现场位于 `testdata/ota-fault-pack/`：原始日志、配置、差异、指标、依赖、盲测输入、真值绑定摘要、实际执行回执及 Git bundle；独立真值由生成器写到本地 private 目录，未公开。SHA 来自实际字节；延迟来自单调时钟实测，重复生成会变化。

公开现场与评分真值严格分离：`public/` 给执行器；`private/labels.json` 只给评分器。真值文件不提交公开仓库，CI 产物也排除 private；**模型输入/工具不能读取它**。开发/留出预先划分，留出是相同机制的不同参数，规模很小，不是六种全新故障，也不证明生产泛化。

## 场景与干扰

| 原因 | 实际注入/测量 |
|---|---|
| 客户端超时过短 | 缩短真实 HTTP 请求 timeout；服务端耗时保持对照值 |
| 服务端变慢 | 服务端实际等待超过正常客户端超时 |
| 网络延迟 | 独立 HTTP 转发 hop 实际延后转发；原服务端仍快 |
| 错误重试放大 | 服务端短暂返回 503；客户端立即重复请求，记录请求数及失败数 |
| 发布清单与包不一致 | 错误 manifest 与实际发布字节哈希不匹配；接收字节与发布者一致 |
| 下载产物损坏 | 发布哈希正确，实际发送损坏字节，接收哈希不同 |

三个超时场景具有相同错误码；完整现场保留 display 心跳和与故障无关的近期 `ui_banner` 变更。发布清单简单场景的**实际错误日志同时记录发布/接收/期望哈希**，故只给三策略相同的紧凑日志快照；其他资产仍归档但不提供额外访问。完整场景全部保留日志、配置、差异、依赖、指标，不为了固定分支命中删干扰。

## 复现及版本来源

```bash
python scripts/benchmark/ota_fault_pack.py --out .regression-results/ota-pack-new

go run ./workers/ai-agent/cmd/agent-bench \
  -input .regression-results/ota-pack-new/public/all.json -preflight-only

# 恢复实际实验的版本仓库；release.git_commit 可直接 git show 核查。
git clone testdata/ota-fault-pack/release-source.bundle /tmp/argus-ota-release-source
git -C /tmp/argus-ota-release-source log --oneline
```

release 的提交来自实验下载程序仓库，**不是车端生产发布记录或 Argus 应用部署提交**。记录 baseline/current commit、配置 blob、配置 SHA256、发布/实际保存产物 SHA256和声明校验值。未成功下载时归档产物是构建参考，`received_artifact_sha256=null`，不能说已接收到这些字节。完整场景的公共 `sources` 将每个 log:N 映射回原文件行或 JSON 指针；拆长记录保留 part，避免工具 400 字符截断。

## 三策略真实模型命令

复用已有 `DEEPSEEK_API_KEY_FILE` 私钥文件配置，**不要将密钥提交或粘贴到报告**。指定同一供应商模型名、端点、价格表，默认温度 0.2、最大输出 1600；记录供应商返回模型名，但供应商只返回别名时不能证明不可变权重版本。

```bash
# 将 DEEPSEEK_API_KEY_FILE 指向已有私有文件。执行前确认供应商支持该模型。
# 不传 -pricing 时成本保持 null；示例不给虚构价格。
for strategy in baseline hybrid agent; do
  go run ./workers/ai-agent/cmd/agent-bench \
    -input .regression-results/ota-pack-new/public/heldout.json \
    -out ".regression-results/ota-live/$strategy" \
    -mode "$strategy" -heldout -trials 3 -parallel 1 \
    -model "$ARGUS_EVAL_MODEL" -base-url https://api.deepseek.com \
    -max-steps 12 -budget 1000000
 done

python scripts/benchmark/score_ota_faults.py \
  --input .regression-results/ota-pack-new/public/heldout.json \
  --truth .regression-results/ota-pack-new/private/labels.json \
  --runs .regression-results/ota-live --trials 3 \
  --out .regression-results/ota-live-score.json
```

`baseline` 确定性获取全部快照行+代码统计后调用一次模型；`hybrid` 原样使用生产 Eligible 和固定取证/调查；`agent` 原生产调查循环。共享上下文上限 24000 字符、预留预算、任务超时 180 秒及供应商最多三次尝试。固定分支保留“只总结观察、上游原因未确认”的生产限制，这可能损害根因表现，评测应暴露它，不能偷偷改提示词掩盖。

统计每次实际 HTTP，包括 429 和传输失败，保存供应商输入/输出/缓存 Token、返回模型、每任务耗时及实际路径。可以用 `-pricing /私有或公开/价格表.json` 传版本化价格表，字段为 `model`, `as_of`, `source`, `input_cached_usd_per_million`, `input_uncached_usd_per_million`, `output_usd_per_million`；每项必须明确，币种 USD。费用是按用量估算，非账单；失败尝试未返回用量、缓存计数缺失且费率不同等情况，整个任务费用保持未知。输入验证/预检不读取密钥或执行模型。

## 评分与解释边界

- 每个预定 case/trial/mode 都计入分母：失败、重试、未决、缺文件不能删掉。缺文件时调用/耗时/费用未知，不当成零；同时提供已记录调用与 P95。
- 主因使用额外的 `cause_code` 枚举，三策略都看到同一类别契约。只统计首要选择，不因报告列举所有原因、提到正确词或置信度高就给分。
- 分别计算已检索、已送入模型、报告引用的证据。逐 HTTP 请求在压缩之后核对完整事实，只有 ID 或截断摘要不算完整证据。无效报告仍保留已送入事实；进一步区分取证不足、压缩后未送入、模型/报告失败与证据具备但推理错误。
- `root_cause_accuracy` 是**受控六类主因识别**；它与关键证据指标一起看，不能单凭类别正确证明完整因果解释正确。输出标注 `semantic_review_status`，真实模型运行后仍需人工检查解释、反证与验证建议。
- OTA v2 契约同时校验行 SHA、包含统计/摘要/来源/任务的完整输入摘要和整个套件版本；旧 SRE 行 SHA 契约兼容。比较器拒绝混用不同输入、模型名、端点、预算或价格表。供应商返回模型名变化也拒绝合并评分；模型只返回别名时明确记录版本未核实。

### ITBench-Lite 分流覆盖预检

现有 ITBench-Lite 适配器输出的每个 case 只有 `id`、`lines`、`sha256`；没有 `error_code_stats` 和 `logs_summary`。生产 `hybrid.Eligible` 要求恰有一种错误码统计及完整的 `log_occurrence` 覆盖摘要，因此这 10 个预先选定的 SRE case **固定分支命中为 0/10，Agent 分支为 10/10**。这是从实际适配器输出契约和生产路由函数逐项核对得到的确定路由结果；无需读取评分标签，也不依赖模型调用。回执见 [SRE 路由预检](sre-routing-preflight-2026-10-14.json)。

这意味着现有 SRE 集可以比较 Agent 与单次调用的诊断质量，但不能证明混合策略降低了 SRE 场景的调用量或费用。原始 ITBench 文件本轮未重新下载，故该结果是适配器 schema 级预检，不是本轮对下载文件执行 CLI 的运行回执。要在 SRE 上测分流收益，需先设计有依据的 SRE 固定取证规则和完整覆盖统计，再重新做盲测；不能把 OTA 错误码规则硬套到 Kubernetes 场景。

## 测试与来源

`test_ota_fault_pack.py` 真正执行三组下载并检查注入/对照/恢复、Git 对象和标签隔离。`test_ota_replay_cli.py` 用本地脚本化 HTTP 响应验证三条真实执行链路与费用回执，**不作为真实模型质量或调用节省证据**。

参考公开工程实践：Datadog [故障真值与现场分离、保留噪声的评测平台](https://www.datadoghq.com/blog/engineering/bits-ai-eval-platform/)（2026-04-07）。这里只借鉴信息隔离与重复现场方法，未声称复现其生产规模。

重试服务的不可用窗口从收到首个真实请求开始计算；生成器检查实际请求放大和全部注入/对照/恢复条件，不满足就报错，不发布可评分输入。

### 2026-10-15 契约核查补充

见[评测分离与独立 SRE 契约](evaluation-contract-audit-2026-10-15.md)。OTA 评分现增加按同一 case/trial 的 hybrid 路径配对的 `summary_by_hybrid_route`，分别统计 fixed/semantic/agent/unknown 四层和全部预定任务；缺少运行回执或有效路径信息的配对归入 unknown，不从分母删除。baseline、hybrid、agent 使用相同 case/trial 集合，总体结果继续保留。真实模型质量、延迟和费用收益仍待实验验证。
