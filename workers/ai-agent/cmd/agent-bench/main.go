// agent-bench executes label-blind benchmark inputs through the production investigation runner.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/hybrid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/investigation"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/nodes"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"go.opentelemetry.io/otel/attribute"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type requestTrace struct {
	rows     []map[string]interface{}
	evidence map[string]string
}

func (t *requestTrace) RoundTrip(r *http.Request) (*http.Response, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	var body map[string]interface{}
	json.Unmarshal(b, &body)
	names := []string{}
	if tools, ok := body["tools"].([]interface{}); ok {
		for _, entry := range tools {
			if obj, ok := entry.(map[string]interface{}); ok {
				if fun, ok := obj["function"].(map[string]interface{}); ok {
					name, _ := fun["name"].(string)
					names = append(names, name)
				}
			}
		}
	}
	row := map[string]interface{}{"tool_choice": body["tool_choice"], "tool_names": names, "supplied_evidence_ids": requestEvidence(body, t.evidence)}
	t.rows = append(t.rows, row)
	start := time.Now()
	response, err := http.DefaultTransport.RoundTrip(r)
	row["duration_ms"] = time.Since(start).Milliseconds()
	if response != nil {
		row["http_status"] = response.StatusCode
	}
	if err != nil {
		row["transport_error"] = true
	}
	if response != nil && response.Body != nil {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
		response.Body.Close()
		if readErr != nil {
			row["response_read_error"] = true
			row["duration_ms"] = time.Since(start).Milliseconds()
			return nil, readErr
		}
		if len(data) > 4*1024*1024 {
			return nil, fmt.Errorf("oversized provider response")
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		var receipt map[string]interface{}
		if json.Unmarshal(data, &receipt) == nil {
			if usage, ok := receipt["usage"].(map[string]interface{}); ok {
				row["usage"] = usage
			}
			if name, ok := receipt["model"].(string); ok {
				row["provider_model"] = name
			}
		}
	}
	row["duration_ms"] = time.Since(start).Milliseconds()
	return response, err
}

type inputCase struct {
	FullInputSHA   string                   `json:"full_input_sha256,omitempty"`
	Sources        []map[string]interface{} `json:"sources,omitempty"`
	SnapshotScope  string                   `json:"snapshot_scope,omitempty"`
	ID             string                   `json:"id"`
	Lines          []string                 `json:"lines"`
	SHA            string                   `json:"sha256"`
	ErrorCodeStats map[string]int           `json:"error_code_stats,omitempty"`
	LogsSummary    string                   `json:"logs_summary,omitempty"`
}
type suite struct {
	Benchmark, Revision, Adapter string
	Cases                        []inputCase
	Partition                    string `json:"partition,omitempty"`
	Task                         string `json:"task,omitempty"`
	InputSchema                  string `json:"input_schema,omitempty"`
}
type repo struct {
	domain.DiagnosisRepository
	lines          []string
	checkpoint     []byte
	errorCodeStats map[string]int
	logsSummary    string
}

func (r repo) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	stats := r.errorCodeStats
	if stats == nil {
		stats = map[string]int{"snapshot_records": len(r.lines)}
	}
	return &domain.AggregatedData{StatsGrain: "log_occurrence", LogsSummary: r.logsSummary, RawLogs: strings.Join(r.lines, "\n"), ErrorCodeStats: stats}, nil
}

func (r *repo) LoadInvestigationCheckpoint(context.Context, string) ([]byte, error) {
	return r.checkpoint, nil
}
func (r *repo) SaveInvestigationCheckpoint(_ context.Context, _ string, b []byte) error {
	r.checkpoint = append([]byte(nil), b...)
	return nil
}

type briefModel struct {
	model.ChatModel
	brief   string
	Replies []*schema.Message
}

func (m *briefModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	copyMessages := append([]*schema.Message(nil), messages...)
	// Task instructions are part of the persisted input, not appended outside its budget.
	reply, err := m.ChatModel.Generate(ctx, copyMessages, opts...)
	if reply != nil {
		m.Replies = append(m.Replies, reply)
	}
	return reply, err
}

// Baseline generation preserves raw replies even when report parsing fails.
// It delegates directly so the single-shot prompt is not augmented by briefModel.
func (m *briefModel) BaselineGenerate(ctx context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, error) {
	reply, err := m.ChatModel.Generate(ctx, messages, options...)
	if reply != nil {
		m.Replies = append(m.Replies, reply)
	}
	return reply, err
}

const brief = `This is an ITBench-Lite SRE transfer evaluation, not vehicle diagnosis. Investigate the Kubernetes incident using only the supplied snapshot evidence. Use read_logs query/offset/limit to inspect more than the first page, and maintain competing hypotheses with gathered evidence. Do not treat downstream alerts as sufficient proof of a root cause. No historical retrieval database is provided. At the end use the required diagnosis JSON schema, and additionally include "entities":[{"name":"namespace/Kind/name","contributing_factor":true,"reason":"evidence-backed explanation"}]. Only list actual root cause candidates, most likely first. root_cause should explain their causal role. Explicitly disclose missing metrics, application logs and traces.`

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	shutdown, e := obs.SetupFromEnv(context.Background(), "argus-agent-bench")
	if e != nil {
		return e
	}
	defer func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = shutdown(ctx)
	}()
	input := flag.String("input", "", "blind JSON input")
	out := flag.String("out", "", "result directory")
	mode := flag.String("mode", "agent", "agent, hybrid, or single-shot baseline")
	trials := flag.Int("trials", 3, "trials per scenario")
	parallel := flag.Int("parallel", 2, "parallel investigations")
	taskPlan := flag.String("task-plan", "", "explicit case/trial replay schedule; source attempts remain immutable")
	heldout := flag.Bool("heldout", false, "require input partition=heldout, with no labels sent to the model")
	fixture := flag.Bool("fixture", false, "run synthetic local HTTP hybrid comparison; out is a JSON file")
	preflight := flag.Bool("preflight-only", false, "validate blind input and report actual production routing without API calls")
	modelName := flag.String("model", "deepseek-flash", "explicit requested provider model; archived with each attempt")
	baseURL := flag.String("base-url", "https://api.deepseek.com", "existing provider endpoint")
	pricingFile := flag.String("pricing", "", "optional dated JSON tariff; missing pricing remains unknown")
	maxSteps := flag.Int("max-steps", 12, "shared maximum planner calls")
	budget := flag.Int("budget", 1000000, "shared conservative reservation ceiling")
	flag.Parse()
	if *fixture {
		if *out == "" {
			return fmt.Errorf("fixture requires -out JSON file")
		}
		return runHybridFixture(*out)
	}
	if *mode != "agent" && *mode != "baseline" && *mode != "hybrid" {
		return fmt.Errorf("invalid mode")
	}
	if *trials < 1 || *trials > 3 || *parallel < 1 || *parallel > 4 || *input == "" || (*out == "" && !*preflight) || *maxSteps < 1 || *maxSteps > 32 || *budget < 1 {
		return fmt.Errorf("invalid benchmark arguments")
	}
	raw, e := os.ReadFile(*input)
	if e != nil {
		return e
	}
	var s suite
	if e = json.Unmarshal(raw, &s); e != nil {
		return e
	}
	if *heldout && s.Partition != "heldout" {
		return fmt.Errorf("independent heldout run requires input partition=heldout")
	}
	if e = validateCases(s); e != nil {
		return e
	}
	taskInstructions, e := taskBrief(s.Task)
	if e != nil {
		return e
	}
	routing := preflightSuite(s)
	if *preflight {
		b, _ := json.MarshalIndent(routing, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	var tariff *priceTable
	if *pricingFile != "" {
		b, err := os.ReadFile(*pricingFile)
		if err != nil {
			return err
		}
		tariff = &priceTable{}
		if err = json.Unmarshal(b, tariff); err != nil {
			return err
		}
		if err = tariff.validate(*modelName); err != nil {
			return err
		}
	}
	var selected map[taskKey]bool
	if *taskPlan != "" {
		data, err := os.ReadFile(*taskPlan)
		if err != nil {
			return err
		}
		selected, err = parseTaskPlan(data, s.Cases, *trials)
		if err != nil {
			return err
		}
	}
	keyBytes, e := os.ReadFile(os.Getenv("DEEPSEEK_API_KEY_FILE"))
	if e != nil {
		return fmt.Errorf("cannot read private API key file")
	}
	key := strings.TrimSpace(string(keyBytes))
	if key == "" {
		return fmt.Errorf("empty API key")
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("output directory already exists; preserve prior evaluation trials")
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	manifest, _ := json.MarshalIndent(map[string]interface{}{"benchmark": s.Benchmark, "revision": s.Revision, "partition": s.Partition, "mode": *mode, "model": *modelName, "provider_endpoint": *baseURL, "trials": *trials, "routing": routing, "max_steps": *maxSteps, "reservation_budget": *budget, "max_context_chars": 24000, "task_deadline_seconds": 180}, "", "  ")
	if e := os.WriteFile(filepath.Join(*out, "run-manifest.json"), manifest, 0644); e != nil {
		return e
	}
	writeErrors := make(chan error, len(s.Cases)*(*trials))
	var wg sync.WaitGroup
	slots := make(chan struct{}, *parallel)
	for _, c := range s.Cases {
		for trial := 0; trial < *trials; trial++ {
			if selected != nil && !selected[taskKey{c.ID, trial}] {
				continue
			}
			slots <- struct{}{}
			wg.Add(1)
			go func(c inputCase, trial int) {
				defer wg.Done()
				defer func() { <-slots }()
				ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				defer cancel()
				start := time.Now()
				batchID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(s.Benchmark+":"+s.Revision+":"+c.ID+fmt.Sprint(trial))).String()
				ctx, endBenchmark := benchmarkContext(ctx, batchID, *mode)
				transport := &requestTrace{evidence: map[string]string{}}
				for i, line := range c.Lines {
					transport.evidence[fmt.Sprintf("log:%d", i+1)] = line
				}
				chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{HTTPClient: &http.Client{Transport: transport, Timeout: 60 * time.Second}, APIKey: key, BaseURL: *baseURL, Model: *modelName})
				var result *domain.DiagnosisResult
				store := &repo{lines: c.Lines, errorCodeStats: c.ErrorCodeStats, logsSummary: c.LogsSummary}
				observer := &briefModel{ChatModel: chat, brief: taskInstructions + fmt.Sprintf(" Snapshot contains %d addressable lines. Do not invent entity names.", len(c.Lines))}
				if err == nil && *mode == "baseline" && s.Task == "ota" {
					result, err = otaBaseline(ctx, observer, c, *budget, *modelName)
				}
				if err == nil && *mode == "baseline" && s.Task != "ota" {
					text := strings.Join(c.Lines, "\n")
					rr := []rune(text)
					if len(rr) > 24000 {
						text = string(rr[:24000])
					}
					var reply *schema.Message
					modelCtx, endModel := obs.Operation(ctx, "model", attribute.String("gen_ai.request.model", *modelName))
					reply, err = observer.BaselineGenerate(modelCtx, []*schema.Message{schema.SystemMessage(brief + ` Return exactly this JSON schema: {"hypothesis_id":"H1","root_cause":"explanation","suggestions":["inspection"],"severity":"high","confidence":0.5,"evidence_ids":[],"limitations":["missing telemetry"],"entities":[{"name":"namespace/Kind/name","contributing_factor":true,"reason":"explanation"}]}. suggestions, evidence_ids, limitations and entities MUST be arrays. severity MUST be high/medium/low/info/critical. No tools are available in this single-shot baseline. Do not fabricate unseen observations.`), schema.UserMessage(text)})
					if reply != nil && reply.ResponseMeta != nil && reply.ResponseMeta.Usage != nil {
						obs.AddTokens(modelCtx, reply.ResponseMeta.Usage.TotalTokens)
					}
					endModel(err)
					if err == nil && reply != nil {
						result, err = nodes.ParseDiagnosisResult(reply.Content)
						if result != nil {
							result.Model = *modelName
							result.ModelSteps = 1
							if reply.ResponseMeta != nil && reply.ResponseMeta.Usage != nil {
								result.TokensUsed = reply.ResponseMeta.Usage.TotalTokens
							}
						}
					}
				}
				if err == nil && (*mode == "agent" || *mode == "hybrid") {
					var a *investigation.Agent
					a, err = investigation.NewAgent(store, nil, observer, investigation.Options{TaskInstructions: taskInstructions + fmt.Sprintf(" Snapshot contains %d addressable lines. Do not invent entity names.", len(c.Lines)), MaxSteps: *maxSteps, MaxReservedTokens: *budget, MaxContextChars: 24000, ModelName: *modelName})
					if err == nil {
						var runner domain.DiagnosisRunner = a
						if *mode == "hybrid" {
							runner, err = hybrid.NewRunner(store, observer, a, hybrid.Options{ModelName: *modelName, MaxReservedTokens: *budget, TaskInstructions: func() string {
								if s.Task == "ota" {
									return taskInstructions
								}
								return ""
							}()})
						}
						if err == nil {
							result, err = runner.Run(ctx, batchID)
						}
					}
				}
				if errors.Is(err, domain.ErrInvestigationBudget) {
					result = &domain.DiagnosisResult{ResolutionStatus: "unresolved", RootCause: "Unresolved: shared budget exhausted", RawLLMOutput: `{"cause_code":"unknown","root_cause":"Unresolved: shared budget exhausted"}`}
					err = nil
				}
				if err == nil && result == nil {
					err = fmt.Errorf("model returned no report")
				}
				var stats llm.GovernorStats
				if g, ok := chat.(*llm.GovernedModel); ok {
					stats = g.Snapshot()
				}
				if len(store.checkpoint) == 0 {
					store.checkpoint = []byte("null")
				}
				traceID := obs.TraceID(ctx)
				if result != nil {
					result.ExecutionTraceID = traceID
				}
				endBenchmark(err)
				status := "success"
				if err != nil {
					status = "failure"
				}
				if result != nil && strings.HasPrefix(result.RootCause, "Unresolved:") {
					status = "unresolved"
				}
				row := map[string]interface{}{"status": status, "partition": s.Partition, "actual_http_calls": len(transport.rows), "cost_usd": nil, "cost_status": "unknown: provider pricing not configured", "request_tool_policy": transport.rows, "checkpoint": json.RawMessage(store.checkpoint), "model_replies": observer.Replies, "provider_stats": stats, "trace_id": traceID, "mode": *mode, "scenario_id": c.ID, "trial": trial, "latency_ms": time.Since(start).Milliseconds(), "model": *modelName, "provider_endpoint": *baseURL, "max_steps": *maxSteps, "reservation_budget": *budget, "max_context_chars": 24000, "input_sha256": c.SHA, "full_input_sha256": c.FullInputSHA, "benchmark_revision": s.Revision, "adapter": s.Adapter, "result": result, "valid_report": err == nil}
				fixedEligible, routeReason := hybrid.Eligible(&domain.AggregatedData{RawLogs: strings.Join(c.Lines, "\n"), ErrorCodeStats: c.ErrorCodeStats, LogsSummary: c.LogsSummary})
				row["fixed_eligible"] = fixedEligible
				row["route_reason"] = routeReason
				row["actual_route"] = *mode
				if *mode == "hybrid" {
					if fixedEligible {
						row["actual_route"] = "fixed"
					} else {
						row["actual_route"] = "agent"
					}
				}
				if *mode == "hybrid" && result != nil && result.Routing != nil {
					row["route_reason"] = result.Routing.Reason
					switch result.Routing.Path {
					case "deterministic":
						row["actual_route"] = "fixed"
					case "semantic":
						row["actual_route"] = "semantic"
					default:
						row["actual_route"] = "agent"
					}
				}
				cost, known := measuredCost(transport.rows, tariff)
				row["cost_usd"] = cost
				row["cost_complete"] = known
				row["pricing"] = tariff
				if known {
					row["cost_status"] = "usage-based tariff estimate; not provider invoice"
				} else {
					row["cost_status"] = "unknown: missing tariff or attempt usage"
				}
				if err != nil {
					row["error"] = strings.ReplaceAll(err.Error(), key, "[REDACTED]")
				}
				dir := filepath.Join(*out, "scenario-"+c.ID, fmt.Sprint(trial), "outputs")
				if e := os.MkdirAll(dir, 0755); e != nil {
					writeErrors <- e
					return
				}
				b, _ := json.MarshalIndent(row, "", "  ")
				if e := os.WriteFile(filepath.Join(dir, "argus_run.json"), b, 0644); e != nil {
					writeErrors <- e
					return
				}
				prediction := json.RawMessage(`{"entities":[],"root_cause":"No valid report"}`)
				if result != nil && err == nil {
					prediction = json.RawMessage(result.RawLLMOutput)
				}
				if e := os.WriteFile(filepath.Join(dir, "agent_output.json"), prediction, 0644); e != nil {
					writeErrors <- e
					return
				}
				fmt.Printf("scenario=%s trial=%d valid=%t latency_ms=%d\n", c.ID, trial, err == nil, time.Since(start).Milliseconds())
			}(c, trial)
		}
	}
	wg.Wait()
	close(writeErrors)
	var errs []error
	for err := range writeErrors {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		if err := writeEvaluationSummary(*out, *mode, s.Partition); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// The benchmark root survives nil diagnosis results, so failed trials can be
// correlated with their exported execution spans and subsequent eval scores.
func benchmarkContext(ctx context.Context, batchID, mode string) (context.Context, func(error)) {
	return obs.Operation(obs.AgentContext(ctx, batchID), "task", attribute.String("argus.benchmark.mode", mode))
}
