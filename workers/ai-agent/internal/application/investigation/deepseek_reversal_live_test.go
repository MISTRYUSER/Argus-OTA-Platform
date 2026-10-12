package investigation

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type liveReversalStore struct {
	checkpointFixture
	logs string
}

func (r *liveReversalStore) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{RawLogs: r.logs, ErrorCodeStats: map[string]int{"E001": 1}}, nil
}
func TestDeepSeekHypothesisReversalLive(t *testing.T) {
	if os.Getenv("ARGUS_LIVE_REVERSAL") != "1" {
		t.Skip("opt-in paid synthetic guided reversal demonstration")
	}
	keyBytes, e := os.ReadFile(os.Getenv("DEEPSEEK_API_KEY_FILE"))
	if e != nil {
		t.Fatal("cannot read private key file")
	}
	key := strings.TrimSpace(string(keyBytes))
	path := os.Getenv("ARGUS_REVERSAL_REPORT_PATH")
	if path == "" {
		t.Fatal("report path required")
	}
	cases := []struct {
		id, logs string
		terms    []string
	}{
		{"fan", "SYNTHETIC E001 CPU temperature=95C, fan tachometer=0RPM despite commanded speed=4000RPM\nSYNTHETIC firmware version/checksum unchanged from stable operation\nSYNTHETIC bench observation: fan mechanically jammed; releasing jam restored RPM and CPU temperature, without software changes", []string{"fan", "风扇"}},
		{"power", "SYNTHETIC E001 CAN timeout during boot; supply voltage drops from12V to8V at timeout\nSYNTHETIC firmware version/checksum unchanged from stable operation\nSYNTHETIC bench observation: loose power terminal; tightening terminal restored12V and timeout disappeared without software changes", []string{"power", "voltage", "电源", "电压", "供电"}},
		{"connector", "SYNTHETIC E001 CAN timeout; supply voltage and temperature normal\nSYNTHETIC firmware version/checksum unchanged from stable operation\nSYNTHETIC bench observation: CAN connector continuity intermittent; reseating connector restored continuity and timeout disappeared without software changes", []string{"connector", "连接", "接头", "接触"}},
	}
	rows := []map[string]interface{}{}
	passed := 0
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		start := time.Now()
		chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: key, BaseURL: "https://api.deepseek.com", Model: "deepseek-flash"})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		preliminaryPrompt := `这是合成的历史案例，不是当前故障证据：旧固件曾导致 E001 超时，升级固件后恢复。请仅根据这个历史案例提出一个待验证初始假设。输出严格JSON {"hypotheses":[{"id":"H1","description":"你的初始判断","status":"open","next_check":"检查当前批次证据"}]}。不要调用工具，不要声称已证明当前根因。`
		preliminary, err := chat.Generate(ctx, []*schema.Message{schema.UserMessage(preliminaryPrompt)}, model.WithToolChoice(schema.ToolChoiceForbidden))
		if err == nil && preliminary == nil {
			err = domain.ErrInvalidDiagnosis
		}
		store := &liveReversalStore{logs: c.logs}
		var initial planInput
		var result *domain.DiagnosisResult
		if err == nil && preliminary != nil {
			err = json.Unmarshal([]byte(preliminary.Content), &initial)
			if err == nil && (len(initial.Hypotheses) != 1 || initial.Hypotheses[0].Status != "open") {
				err = domain.ErrInvalidDiagnosis
			}
		}
		if err == nil {
			a, createErr := NewAgent(store, nil, chat, Options{MaxSteps: 8, ModelName: "deepseek-flash", TaskInstructions: "本轮延续模型已经提出的历史案例初始假设。现在自主读取当前批次日志与统计来验证；历史案例只是参考。若实际证据反驳初始假设，调用update_investigation_plan标记refuted并提出替代假设。依据实际证据完成报告，不要坚持旧判断。"})
			err = createErr
			if err == nil {
				id := uuid.NewString()
				tokens := 0
				if preliminary.ResponseMeta != nil && preliminary.ResponseMeta.Usage != nil {
					tokens = preliminary.ResponseMeta.Usage.TotalTokens
				}
				state := &agentState{batchID: id, phase: "plan", step: 1, tokens: tokens, reservedTokens: 3 * (len(preliminaryPrompt) + 8192 + 1600), hypotheses: initial.Hypotheses, evidence: map[string]domain.EvidenceItem{}, used: map[string]bool{}, limitations: map[string]string{}, artifacts: map[string]json.RawMessage{}, toolArtifacts: map[string]string{}, messages: []*schema.Message{schema.SystemMessage(systemPrompt), schema.UserMessage(preliminaryPrompt + "\n" + a.options.TaskInstructions), preliminary}}
				if err = a.saveCheckpoint(ctx, state); err == nil {
					result, err = a.Run(ctx, id)
				}
			}
		}
		reversal := false
		correct := false
		if result != nil {
			for _, h := range result.Hypotheses {
				if h.ID == initial.Hypotheses[0].ID && h.Status == "refuted" && len(h.Refutes) > 0 && result.HypothesisID != h.ID {
					reversal = true
				}
			}
			for _, term := range c.terms {
				if strings.Contains(strings.ToLower(result.RootCause), term) {
					correct = true
				}
			}
		}
		pass := err == nil && reversal && correct
		if pass {
			passed++
		}
		row := map[string]interface{}{"id": c.id, "kind": "guided synthetic live demonstration, not a professional benchmark score", "initial_hypotheses": initial.Hypotheses, "initial_hypothesis_origin": "actual DeepSeek generation from advisory historical case only; no current logs in preliminary request", "logs": c.logs, "result": result, "reversed_with_refutation_evidence": reversal, "fixture_cause_match": correct, "pass": pass, "latency_ms": time.Since(start).Milliseconds(), "provider_stats": chat.(*llm.GovernedModel).Snapshot()}
		if err != nil {
			row["error"] = strings.ReplaceAll(err.Error(), key, "[REDACTED]")
		}
		rows = append(rows, row)
		cancel()
	}
	report := map[string]interface{}{"cases": rows, "passed": passed, "total": len(cases), "limitations": "Guided synthetic demonstrations with explicit physical bench observations; not an estimate of production diagnostic accuracy."}
	b, _ := json.MarshalIndent(report, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0755)
	if e = os.WriteFile(path, b, 0644); e != nil {
		t.Fatal(e)
	}
	t.Logf("actual model-generated hypothesis reversal: %d/%d synthetic demonstrations", passed, len(cases))
	if passed == 0 {
		t.Fatal("no real model reversal observed")
	}
}
