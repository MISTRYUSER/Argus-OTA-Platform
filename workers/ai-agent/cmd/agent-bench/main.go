// agent-bench executes label-blind benchmark inputs through the production investigation runner.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/investigation"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/nodes"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type requestTrace struct{ rows []map[string]interface{} }

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
	t.rows = append(t.rows, map[string]interface{}{"tool_choice": body["tool_choice"], "tool_names": names})
	return http.DefaultTransport.RoundTrip(r)
}

type inputCase struct {
	ID    string   `json:"id"`
	Lines []string `json:"lines"`
	SHA   string   `json:"sha256"`
}
type suite struct {
	Benchmark, Revision, Adapter string
	Cases                        []inputCase
}
type repo struct {
	domain.DiagnosisRepository
	lines      []string
	checkpoint []byte
}

func (r repo) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{RawLogs: strings.Join(r.lines, "\n"), ErrorCodeStats: map[string]int{"snapshot_records": len(r.lines)}}, nil
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

const brief = `This is an ITBench-Lite SRE transfer evaluation, not vehicle diagnosis. Investigate the Kubernetes incident using only the supplied snapshot evidence. Use read_logs query/offset/limit to inspect more than the first page, and maintain competing hypotheses with gathered evidence. Do not treat downstream alerts as sufficient proof of a root cause. No historical retrieval database is provided. At the end use the required diagnosis JSON schema, and additionally include "entities":[{"name":"namespace/Kind/name","contributing_factor":true,"reason":"evidence-backed explanation"}]. Only list actual root cause candidates, most likely first. root_cause should explain their causal role. Explicitly disclose missing metrics, application logs and traces.`

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("input", "", "blind JSON input")
	out := flag.String("out", "", "result directory")
	mode := flag.String("mode", "agent", "agent or single-shot baseline")
	trials := flag.Int("trials", 3, "trials per scenario")
	parallel := flag.Int("parallel", 2, "parallel investigations")
	flag.Parse()
	if *mode != "agent" && *mode != "baseline" {
		return fmt.Errorf("invalid mode")
	}
	if *trials < 1 || *trials > 3 || *parallel < 1 || *parallel > 4 || *input == "" || *out == "" {
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
	if len(s.Cases) == 0 || len(s.Cases) > 35 {
		return fmt.Errorf("expected 1..35 scenarios")
	}
	for _, c := range s.Cases {
		if _, err := strconv.Atoi(c.ID); err != nil {
			return fmt.Errorf("invalid scenario id")
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(c.Lines, "\n"))))
		if digest != c.SHA {
			return fmt.Errorf("input hash mismatch: %s", c.ID)
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
	writeErrors := make(chan error, len(s.Cases)*(*trials))
	var wg sync.WaitGroup
	slots := make(chan struct{}, *parallel)
	for _, c := range s.Cases {
		for trial := 0; trial < *trials; trial++ {
			slots <- struct{}{}
			wg.Add(1)
			go func(c inputCase, trial int) {
				defer wg.Done()
				defer func() { <-slots }()
				ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				defer cancel()
				start := time.Now()
				transport := &requestTrace{}
				chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{HTTPClient: &http.Client{Transport: transport, Timeout: 60 * time.Second}, APIKey: key, BaseURL: "https://api.deepseek.com", Model: "deepseek-flash"})
				var result *domain.DiagnosisResult
				store := &repo{lines: c.Lines}
				observer := &briefModel{ChatModel: chat, brief: brief + fmt.Sprintf(" Snapshot contains %d addressable lines. Do not invent entity names.", len(c.Lines))}
				if err == nil && *mode == "baseline" {
					text := strings.Join(c.Lines, "\n")
					rr := []rune(text)
					if len(rr) > 24000 {
						text = string(rr[:24000])
					}
					var reply *schema.Message
					reply, err = chat.Generate(ctx, []*schema.Message{schema.SystemMessage(brief + ` Return exactly this JSON schema: {"hypothesis_id":"H1","root_cause":"explanation","suggestions":["inspection"],"severity":"high","confidence":0.5,"evidence_ids":[],"limitations":["missing telemetry"],"entities":[{"name":"namespace/Kind/name","contributing_factor":true,"reason":"explanation"}]}. suggestions, evidence_ids, limitations and entities MUST be arrays. severity MUST be high/medium/low/info/critical. No tools are available in this single-shot baseline. Do not fabricate unseen observations.`), schema.UserMessage(text)})
					if err == nil && reply != nil {
						result, err = nodes.ParseDiagnosisResult(reply.Content)
						if result != nil {
							result.Model = "deepseek-flash"
							result.ModelSteps = 1
							if reply.ResponseMeta != nil && reply.ResponseMeta.Usage != nil {
								result.TokensUsed = reply.ResponseMeta.Usage.TotalTokens
							}
						}
					}
				}
				if err == nil && *mode == "agent" {
					var a *investigation.Agent
					a, err = investigation.NewAgent(store, nil, observer, investigation.Options{TaskInstructions: brief + fmt.Sprintf(" Snapshot contains %d addressable lines. Do not invent entity names.", len(c.Lines)), MaxSteps: 12, ModelName: "deepseek-flash"})
					if err == nil {
						result, err = a.Run(ctx, uuid.NewSHA1(uuid.NameSpaceURL, []byte("itbench:"+s.Revision+":"+c.ID+fmt.Sprint(trial))).String())
					}
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
				row := map[string]interface{}{"request_tool_policy": transport.rows, "checkpoint": json.RawMessage(store.checkpoint), "model_replies": observer.Replies, "provider_stats": stats, "mode": *mode, "scenario_id": c.ID, "trial": trial, "latency_ms": time.Since(start).Milliseconds(), "model": "deepseek-flash", "input_sha256": c.SHA, "benchmark_revision": s.Revision, "adapter": s.Adapter, "result": result, "valid_report": err == nil}
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
	return errors.Join(errs...)
}
