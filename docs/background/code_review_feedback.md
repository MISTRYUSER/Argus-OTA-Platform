# 字节跳动架构师 Code Review 反馈与优化建议

**日期**: 2026-01-31
**审阅人**: 字节跳动资深后端架构师（LLM 应用方向）
**项目**: Argus OTA Platform - AI Agent Worker
**框架**: Eino v0.7.28

---

## 概述

这是一份来自字节跳动资深后端架构师的专业 Code Review，针对我们的 Eino Multi-Agent 设计提出了 7 个关键改进点。反馈质量极高，涵盖了架构模式、框架集成、RAG 优化、容灾策略、Prompt 工程、代码细节、评估体系等方方面面。

---

## 核心问题总结

### 问题 1：架构模式误用 ⚠️⚠️⚠️

**反馈内容**：
> "你标注为 Supervisor-Worker，但实际是 Sequential Workflow（线性流水线）"

**问题解析**：
- **真正的 Supervisor**：有一个中心大脑，根据中间结果动态决策
  ```
  LLM 判断 → confidence < 0.5 → 去查 RAG
             → confidence > 0.8 → 直接输出
  ```
- **我们当前的 Graph**：固定的线性流程
  ```
  DataLoader → RAG → LLM → END
  ```

**修正方案**：
1. ✅ **术语修正**：将架构模式改称为 "RAG Pipeline" 或 "Sequential Graph"
2. 🔄 **未来扩展**：保留动态决策的接口（如 Condition Node）

**面试风险**：
> 面试官问："你的 Supervisor 怎么做动态决策？"
> 你答不上来（因为根本没有动态决策）

---

### 问题 2：没用 Eino 的 Model 接口 ⚠️⚠️⚠️（最关键！）

**反馈内容**：
> "手写 GLMClient 是在 Eino 框架外裸奔，失去了可观测性和统一治理"

**问题解析**：
```go
// ❌ 当前做法：裸 HTTP 调用
func (c *GLMClient) Diagnose(...) {
    req, _ := http.NewRequest(...)
    resp, _ := c.httpClient.Do(req)
    // 自己处理日志、重试、监控
}

// ✅ 应该这样做：使用 Eino 的 Model 接口
func (c *EinoGLMClient) Diagnose(...) {
    // Eino 自动处理：Token 统计、Tracing、重试、熔断
}
```

**损失的功能**：
- ❌ Token 使用统计（无法计算成本）
- ❌ 链路追踪（无法监控性能）
- ❌ 统一重试策略（每个接口都要自己写）

**修正方案**：
查看 Eino 的 `components/model` 接口，实现它而不是裸 HTTP

---

### 问题 3：RAG 优化点

#### 3.1 向量归一化

**反馈**：
> "你用的是 Cosine Distance (<=>)，但检查 Embedding 向量是否已归一化"

**解析**：
```sql
-- 你用的是 Cosine Distance
ORDER BY embedding <=> ?

-- 但检查一下：你的 Embedding 向量是否已归一化？
-- 如果没有，应该用 Inner Product
ORDER BY embedding <#>
```

**建议**：
- 查看 GLM Embedding API 文档
- 确认输出是否已归一化
- 如果是，用 `<=>`；如果不是，用 `<#>` 或先归一化

#### 3.2 Re-ranking（重排序）

**反馈**：
> "标准流程：向量检索召回 Top-50（快速但粗糙）→ Re-ranker 精排 Top-5（慢但准确）"

**建议**：
- v1.0：不考虑（需要额外模型）
- v2.0：可以加 LLM Self-Consistency（让 LLM 判断相关性）

---

### 问题 4：降级策略细节

**反馈**：
> "RAG 降级时，应该告诉 LLM '知识库不可用'，避免幻觉"

**当前代码**：
```go
if err != nil {
    state.RAGCases = []SimilarCase{} // 空列表
    return state, nil
}
```

**问题**：
LLM 不知道 RAG 是"失败了"还是"真的没搜到"

**建议改进**：
```go
if err != nil {
    state.RAGCases = []SimilarCase{}
    state.RAGUnavailable = true  // ✅ 新增标记
    return state, nil
}
```

**Prompt 对应修改**：
```text
## 相似历史案例
{{if .RAGUnavailable}}
⚠️ 知识库当前不可用（数据库连接失败），请仅根据通用知识诊断，并降低 confidence 到 0.5 以下。
{{else if .RAGCases}}
检索到 {{len .RAGCases}} 条相似案例...
{{else}}
未检索到相似案例...
{{end}}
```

---

### 问题 5：Prompt 优化

#### 5.1 JSON Mode

**反馈**：
> "GLM-4 和 GPT-4 都支持 JSON Mode。除了在 Prompt 里喊话，建议在 API 调用参数中显式开启"

**改进代码**：
```go
payload := map[string]interface{}{
    "model": "glm-4",
    "messages": messages,
    "response_format": map[string]string{"type": "json_object"},  // ✅ 强制 JSON
}
```

**收益**：
- 减少 `CleanJSON` 解析失败的概率
- GLM-4 支持这个参数（查看文档确认）

#### 5.2 思维链（CoT）

**反馈**：
> "对于复杂的车辆诊断，建议要求 LLM 先输出 `<analysis>` 标签块进行推理，最后输出 JSON"

**改进 Prompt**：
```text
请按以下格式输出：

## 分析过程
<analysis>
1. 错误码 E_CAN_TIMEOUT 通常表示...
2. 结合故障日志，发现...
3. 因此根本原因可能是...
</analysis>

## 诊断结果
{
  "root_cause": "...",
  "suggestions": [...],
  "severity": "high",
  "confidence": 0.85
}
```

**收益**：
- 强制 LLM 先思考，提高准确率
- `<analysis>` 可以展示给用户（透明度）

---

### 问题 6：代码细节

#### 6.1 CleanJSON 用正则

**反馈**：
> "字符串切割处理 Markdown 代码块的方式虽然常用，但在生产环境建议用正则"

**改进代码**：
```go
import "regexp"

var jsonBlockRegex = regexp.MustCompile(`(?s)```(?:json)?\s*(.*?)\s*````)

func CleanJSON(raw string) string {
    match := jsonBlockRegex.FindStringSubmatch(raw)
    if len(match) > 1 {
        return match[1]
    }
    return raw
}
```

#### 6.2 动态 Prompt

**反馈**：
> "Prompt 是需要频繁热更的配置，建议放入配置中心或数据库"

**建议**：
```go
func (s *LLMNode) loadPromptFromDB(ctx context.Context, version string) (string, error) {
    var prompt string
    s.db.QueryRow("SELECT content FROM prompts WHERE version = ?", version).Scan(&prompt)
    return prompt, nil
}
```

---

### 问题 7：评估体系

**反馈**：
> "没有评估就没有优化"

**建议**：

**Phase 4.5: 评估体系搭建**

```go
// 准备 20-50 个 Golden Cases
type GoldenCase struct {
    Input          string  // 故障日志
    GoldenAnswer   string  // 标准答案
    ExpectedRootCause string
}

// 评估指标
func Evaluate(diagnosisResult, goldenAnswer string) {
    // 1. Retrieval Recall: RAG 有没有搜到正确案例？

    // 2. Diagnosis Accuracy: LLM 诊断和标准答案的语义相似度
}
```

---

## 改进优先级（按重要性排序）

### P0（必须改，影响架构）
1. ✅ **术语修正**：Supervisor → Sequential Graph
2. ✅ **接入 Eino Model 接口**：替换裸 HTTP 调用

### P1（强烈建议，影响质量）
3. ✅ **降级标记**：`RAGUnavailable`
4. ✅ **JSON Mode**：显式开启
5. ✅ **思维链**：`<analysis>` 标签

### P2（建议，影响体验）
6. ✅ **CleanJSON 正则**：更稳健
7. ✅ **Token 截断优化**：保留首尾日志
8. ✅ **动态 Prompt**：支持热更新

### P3（可选，未来迭代）
9. 🔄 **Re-ranking**：Top-50 → Top-5
10. 🔄 **评估体系**：Golden Cases

---

## 关键收获

### 1. 架构认知升级
- **Sequential vs Supervisor**：本质区别在于"是否动态决策"
- **Eino Model 接口**：不是简单的 HTTP 封装，而是提供了可观测性、治理能力

### 2. 工程化思维
- **降级策略细节**：不仅要降级，还要告诉下游"为什么降级"
- **评估驱动优化**：没有 Golden Cases 就谈不上优化

### 3. Prompt 工程深度
- **JSON Mode**：API 参数比 Prompt 指令更可靠
- **CoT（思维链）**：强制思考能显著提高准确率

---

## 下一步行动

1. ✅ **立即修正术语**：将 "Supervisor-Worker" 改为 "Sequential Graph"
2. ✅ **研究 Eino Model 接口**：查看 `components/model` 的定义
3. ✅ **实现改进版本**：基于这些建议修正设计文档

---

## 面试加分点

### Q: 为什么不用 Eino 的 Model 接口？
```
A: 我最初的设计是裸 HTTP 调用 GLM API，这导致失去了 Eino 框架的核心价值——
   可观测性和统一治理。通过接入 Eino 的 Model 接口，我能自动获得：
   - Token 统计（成本控制）
   - 链路追踪（性能监控）
   - 统一重试和熔断（高可用）
   这是框架设计的高级思考。
```

### Q: 你的 Supervisor 怎么做动态决策？
```
A: 实际上我当前的架构是 Sequential Graph（线性流水线），不是真正的 Supervisor。
   Supervisor 模式需要有一个中心大脑根据中间结果动态决策，
   比如 confidence < 0.5 时查 RAG，> 0.8 时直接输出。
   v1.0 版本我采用了线性流程，v2.0 会引入动态决策。
```

---

**文档维护者**: AI Agent Team
**最后更新**: 2026-01-31
