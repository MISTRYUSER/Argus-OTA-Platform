# Supervisor-Worker 架构设计文档

> **项目**: Argus OTA Platform - AI Agent Worker
> **版本**: v2.0 (Supervisor 模式)
> **日期**: 2026-02-08

---

## 核心理念

**Supervisor-Worker 架构** 是一种多 Agent 协作模式，其中：

- **Supervisor（中控）**：持有全局状态，动态决策调用哪个 Worker
- **Workers（专家）**：执行具体任务，完成后将控制权交还给 Supervisor
- **控制权回收**：每个 Worker 执行完后必须返回 Supervisor，避免 Worker 之间"踢皮球"

---

## 数据流向图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           Supervisor-Worker 架构                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│   ┌─────────┐                                                            │
│   │  START  │                                                            │
│   └────┬────┘                                                            │
│        │                                                                 │
│        ▼                                                                 │
│   ┌─────────────────┐     ┌─────────────┐                              │
│   │   SUPERVISOR    │────▶│  Decision    │─── next_worker            │
│   │   (决策中心)      │     │  (Decide)    │     instruction            │
│   └────────┬────────┘     └─────────────┘                              │
│            │                                                              │
│            ▼ (动态分支)                                                   │
│   ┌──────────────────────────────────────┐                              │
│   │                                      │                              │
│   ▼                                      ▼                              │
│ ┌─────────┐    ┌─────────┐    ┌─────────┐                            │
│ │  Guard  │    │  Loader │    │   RAG   │                            │
│ │ (合规)   │    │ (加载)  │    │ (检索)  │                            │
│ └────┬────┘    └────┬────┘    └────┬────┘                            │
│      │               │               │                                  │
│      └───────────────┼───────────────┘                                  │
│                      ▼                                                  │
│              ┌──────────────┐                                         │
│              │   Reasoning  │                                         │
│              │   (推理)     │                                         │
│              └──────┬───────┘                                         │
│                     │                                                 │
│                     ▼ (控制权回收)                                     │
│              ┌──────────────┐                                         │
│              │   SUPERVISOR  │ ◄── 所有 Worker 执行完                 │
│              │  (重新决策)    │     后必须返回                          │
│              └──────┬───────┘                                         │
│                     │                                                 │
│                     ▼                                                 │
│              ┌──────────────┐                                         │
│              │   Terminal    │                                         │
│              │   (输出/终止)  │                                         │
│              └──────┬───────┘                                         │
│                     │                                                 │
│                     ▼                                                 │
│              ┌──────────────┐                                         │
│              │     END       │                                         │
│              └──────────────┘                                         │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 核心组件

### 1. DiagnosisContext（全局状态）

```go
type DiagnosisContext struct {
    // 基础元数据
    TaskID  string
    BatchID string

    // 输入数据
    AggregatedData *AggregatedData

    // 中间产物
    RAGCases       []SimilarCase
    RAGUnavailable bool

    // 最终结果
    DiagnosisResult *DiagnosisResult

    // 流程控制
    ProcessingStatus StatusEnum
    ErrorMessage     string
    Confidence       float64
    NextStep         string

    // ────────────────────────────────────────────────
    // Supervisor 模式专用字段
    // ────────────────────────────────────────────────
    CurrentWorker    string        // 当前正在执行的 Worker
    LastWorker       string        // 上一个执行的 Worker
    WorkerHistory    []string      // 执行历史
    MaxIterations    int           // 最大迭代次数
    CurrentIteration int           // 当前迭代次数
    Instruction      string        // Supervisor 给 Worker 的指令
    StartTime        time.Time     // 流程开始时间
    WorkerTimings    map[string]time.Duration // 执行时间统计
}
```

### 2. SupervisorAgent（中央调度器）

```go
type SupervisorAgent struct {
    HighConfidenceThreshold float64 // 高置信度阈值（>0.8 直接输出）
    LowConfidenceThreshold  float64 // 低置信度阈值（<0.5 需要 RAG）
    MaxRAGRetries           int     // RAG 最大重试次数
}

func (s *SupervisorAgent) Decide(ctx context.Context, state *DiagnosisContext) *Decision {
    // 根据 state.LastWorker 和 state.ProcessingStatus 决定下一步
    // 返回 Decision 包含：NextWorker, Instruction, ShouldEnd, EndReason
}
```

**决策逻辑**：

| LastWorker | 条件 | NextWorker | 说明 |
|------------|------|------------|------|
| (空) | - | guard | 初始状态，先检查合规性 |
| guard | 通过 | loader | 合规检查通过，加载数据 |
| guard | 失败 | terminal | 合规检查失败，终止 |
| loader | confidence >= 0.8 | reasoning | 高置信度，直接推理（快通道） |
| loader | confidence < 0.8 | rag | 低置信度，需要 RAG |
| rag | 查到案例 | reasoning | RAG 成功，进入推理 |
| rag | 未查到 | rag / reasoning | 重试或降级 |
| reasoning | success | terminal | 完成，结束 |
| reasoning | confidence < 0.5 | rag | 置信度低，再查 RAG |

### 3. Workers（专家节点）

| Worker | 职责 | 输入 | 输出 |
|--------|------|------|------|
| **GuardWorker** | 合规检查 | BatchID | ProcessingStatus |
| **LoaderWorker** | 加载数据 | BatchID | AggregatedData, Confidence |
| **RAGWorker** | 向量检索 | ErrorCodes, Logs | SimilarCase[] |
| **ReasoningWorker** | LLM 推理 | AggregatedData, RAGCases | DiagnosisResult |
| **TerminalWorker** | 终止处理 | - | 统计信息 |

---

## 关键特性

### 1. 控制权回收（Deterministic Transfer）

**传统模式的问题**：
- Worker A → Worker B → Worker C...
- 一旦流程开始，Supervisor 失去控制
- Worker 之间可能"踢皮球"，形成死循环

**Supervisor 模式的解决方案**：
```
Supervisor → Worker → Supervisor → Worker → Supervisor → ...
                  ↑                        ↑
                  └────────────────────────┘
                    控制权回收
```

**代码实现**：
```go
// 所有 Worker 执行完后都回到 Supervisor
graph.AddEdge("guard", "supervisor")
graph.AddEdge("loader", "supervisor")
graph.AddEdge("rag", "supervisor")
graph.AddEdge("reasoning", "supervisor")
```

### 2. 防止无限循环

```go
func (c *DiagnosisContext) ShouldTerminate() bool {
    // 条件1：达到最大迭代次数
    if c.CurrentIteration >= c.MaxIterations {
        return true
    }

    // 条件2：检测循环（同一个 Worker 连续执行 3 次）
    if len(c.WorkerHistory) >= 6 {
        last6 := c.WorkerHistory[len(c.WorkerHistory)-6:]
        if last6[0] == last6[2] && last6[2] == last6[4] &&
           last6[1] == last6[3] && last6[3] == last6[5] &&
           last6[0] != last6[1] {
            return true  // 检测到 A-B-A-B 循环
        }
    }

    return false
}
```

### 3. 动态分支

```go
supervisorBranch := compose.NewGraphBranch(
    func(ctx context.Context, state *DiagnosisContext) (string, error) {
        if state.NextStep == compose.END || state.ShouldTerminate() {
            return "terminal", nil
        }
        return state.NextStep, nil  // Supervisor 决定的 Worker
    },
    map[string]bool{
        "guard": true,
        "loader": true,
        "rag": true,
        "reasoning": true,
        "terminal": true,
    },
)
```

---

## 使用示例

```go
// 创建 Supervisor Graph
graph, err := supervisor.NewSupervisorGraph(
    diagnosisRepo,
    vectorRetriever,
    llmConfig,
)

// 可视化架构
graph.PrintGraph()

// 运行
result, err := graph.Run(ctx, batchID)
```

**执行日志示例**：
```
[Supervisor] Decision:  → guard (检查请求是否合规)
[GuardWorker] Starting guard checks for batch 123
[GuardWorker] ✅ All guards passed
[Supervisor] Decision: guard → loader (加载批次数据)
[LoaderWorker] Loading data for batch 123
[LoaderWorker] ✅ Data loaded, confidence: 0.60
[Supervisor] Decision: loader → rag (置信度不足，检索相似历史案例)
[RAGWorker] Searching for similar cases (confidence: 0.60)
[RAGWorker] ✅ Found 3 similar cases
[Supervisor] Decision: rag → reasoning (基于 3 条相似案例进行推理)
[ReasoningWorker] Starting diagnosis (has RAG: true)
[ReasoningWorker] ✅ Diagnosis completed, confidence: 0.82
[Supervisor] Decision: reasoning → terminal (Diagnosis completed successfully)
[TerminalWorker] =======================================
[TerminalWorker] Workflow completed
[TerminalWorker] Status: SUCCESS
[TerminalWorker] Iterations: 4
[TerminalWorker] Total time: 1.2s
[TerminalWorker]   supervisor: 10ms
[TerminalWorker]   guard: 5ms
[TerminalWorker]   loader: 200ms
[TerminalWorker]   rag: 800ms
[TerminalWorker]   reasoning: 185ms
[TerminalWorker] =======================================
```

---

## 面试话术模板

**Q: 你的 AI 编排是怎么做的？**

> "我采用了 **Supervisor-Worker（中控-专家）架构**，而不是简单的线性流程。
>
> 系统的核心是一个 **Supervisor Agent**，它维护着全局的诊断上下文。所有的具体任务——比如'去向量库检索'或'进行逻辑推理'——都被封装成了独立的 **Worker**。
>
> 这里的关键是 **'控制权回收'**：Supervisor 派单给 Worker 去执行任务，Worker 完成后**必须**把结果交还给 Supervisor。如果 Supervisor 发现 RAG 返回的置信度不够，它会**现场决策**，要么让 Worker 重试，要么降级调用通用大模型。这让系统具备了**自我修正**的能力。
>
> 为了防止无限循环，我实现了双重保护机制：最大迭代次数限制 + 循环检测算法。"
>
> **技术亮点**：
> - 使用 Eino 的 `GraphBranch` 实现动态分支
> - 通过 `DiagnosisContext` 在节点间流转状态
> - 每个 Worker 返回 `*compose.Lambda` 供 Graph 编排

---

## 文件结构

```
workers/ai-agent/internal/application/supervisor/
├── supervisor_agent.go    # Supervisor Agent（决策逻辑）
├── supervisor_graph.go    # Graph 编排（边和分支）
└── workers.go             # Workers 实现（Guard, Loader, RAG, Reasoning, Terminal）
```

---

## 架构演进

```
v1.0 Sequential Graph（当前）
  固定流程：DataLoader → RAG → LLM → END

v2.0 Supervisor Graph（本文档）
  动态调度：Supervisor 根据状态决策 Worker
  控制权回收：Worker 完成后返回 Supervisor

v3.0 LLM-Driven Supervisor（未来）
  LLM 驱动决策：Supervisor 使用 LLM 进行决策
  自主规划：Supervisor 根据复杂情况自主选择路径
```

---

**文档版本**: v2.0
**最后更新**: 2026-02-08
**作者**: AI Agent Team
