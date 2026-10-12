package investigation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"sort"
)

const checkpointPolicy = "argus-harness-v3"

type checkpoint struct {
	TaskInstructions                                   string
	Version                                            int
	Policy, Model, BatchID, Phase                      string
	MaxSteps, Step, Tokens, NextTool                   int
	MaxReservedTokens, ReservedTokens, MaxContextChars int
	Messages                                           []*schema.Message
	Response                                           *schema.Message
	Observations                                       []domain.ToolObservation
	Evidence                                           map[string]domain.EvidenceItem
	Used                                               map[string]bool
	Limitations                                        map[string]string
	Hypotheses                                         []domain.Hypothesis
	Artifacts                                          map[string]json.RawMessage
	ToolArtifacts                                      map[string]string
	Result                                             *domain.DiagnosisResult
	Compactions                                        int
}

func (a *Agent) saveCheckpoint(ctx context.Context, s *agentState) error {
	store, ok := a.repo.(domain.InvestigationCheckpointStore)
	if !ok {
		return nil
	}
	data, err := json.Marshal(checkpoint{TaskInstructions: a.options.TaskInstructions, Version: 1, Policy: checkpointPolicy, Model: a.options.ModelName, BatchID: s.batchID, Phase: s.phase, MaxSteps: a.options.MaxSteps, MaxReservedTokens: a.options.MaxReservedTokens, ReservedTokens: s.reservedTokens, MaxContextChars: a.options.MaxContextChars, Step: s.step, Tokens: s.tokens, NextTool: s.nextTool, Messages: s.messages, Response: s.response, Observations: s.observations, Evidence: s.evidence, Used: s.used, Limitations: s.limitations, Hypotheses: s.hypotheses, Artifacts: s.artifacts, ToolArtifacts: s.toolArtifacts, Result: s.result, Compactions: s.compactions})
	if err != nil {
		return err
	}
	return store.SaveInvestigationCheckpoint(ctx, s.batchID, data)
}
func (a *Agent) loadCheckpoint(ctx context.Context, s *agentState) error {
	store, ok := a.repo.(domain.InvestigationCheckpointStore)
	if !ok {
		return nil
	}
	data, err := store.LoadInvestigationCheckpoint(ctx, s.batchID)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if len(data) > 1024*1024 {
		return fmt.Errorf("oversized checkpoint")
	}
	var c checkpoint
	if err = json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.TaskInstructions != a.options.TaskInstructions || c.Version != 1 || c.Policy != checkpointPolicy || c.Model != a.options.ModelName || c.BatchID != s.batchID || c.MaxSteps != a.options.MaxSteps || c.MaxReservedTokens != a.options.MaxReservedTokens || c.MaxContextChars != a.options.MaxContextChars {
		return fmt.Errorf("checkpoint policy/model/batch mismatch")
	}
	if c.ReservedTokens < 0 || c.ReservedTokens > a.options.MaxReservedTokens || c.Tokens < 0 {
		return fmt.Errorf("invalid checkpoint budget")
	}
	if c.Step < 0 || c.Step > a.options.MaxSteps || c.NextTool < 0 {
		return fmt.Errorf("invalid checkpoint counters")
	}
	switch c.Phase {
	case "plan", "tools", "feedback", "finish", "completed":
	default:
		return fmt.Errorf("invalid checkpoint phase")
	}
	if c.Phase == "tools" && (c.Response == nil || c.NextTool > len(c.Response.ToolCalls)) {
		return fmt.Errorf("invalid pending tool cursor")
	}
	s.phase = c.Phase
	s.step = c.Step
	s.tokens = c.Tokens
	s.reservedTokens = c.ReservedTokens
	s.nextTool = c.NextTool
	s.messages = c.Messages
	s.response = c.Response
	s.observations = c.Observations
	s.evidence = c.Evidence
	s.used = c.Used
	s.limitations = c.Limitations
	s.hypotheses = c.Hypotheses
	s.result = c.Result
	s.compactions = c.Compactions
	if c.Artifacts != nil {
		s.artifacts = c.Artifacts
	}
	if c.ToolArtifacts != nil {
		s.toolArtifacts = c.ToolArtifacts
	}
	if s.evidence == nil {
		s.evidence = map[string]domain.EvidenceItem{}
	}
	if s.used == nil {
		s.used = map[string]bool{}
	}
	if s.limitations == nil {
		s.limitations = map[string]string{}
	}
	return nil
}
func (a *Agent) archive(ctx context.Context, s *agentState, output string) (string, error) {
	if store, ok := a.repo.(domain.InvestigationArtifactStore); ok {
		return store.PutInvestigationArtifact(ctx, s.batchID, []byte(output))
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(output)))
	s.artifacts[id] = json.RawMessage(output)
	return id, nil
}

type artifactInput struct {
	ArtifactID string `json:"artifact_id"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

func (a *Agent) artifactTool(s *agentState) (tool.InvokableTool, error) {
	return utils.InferTool("read_evidence_artifact", "Read a bounded page of an archived tool output from this batch only. Use artifact_id returned by compacted tool results.", func(ctx context.Context, in *artifactInput) (map[string]interface{}, error) {
		if in == nil || len(in.ArtifactID) != 64 || in.Offset < 0 {
			return nil, fmt.Errorf("invalid artifact request")
		}
		var data []byte
		var err error
		if store, ok := a.repo.(domain.InvestigationArtifactStore); ok {
			data, err = store.GetInvestigationArtifact(ctx, s.batchID, in.ArtifactID)
		} else {
			data = s.artifacts[in.ArtifactID]
			if data == nil {
				err = fmt.Errorf("artifact not found")
			}
		}
		if err != nil {
			return nil, err
		}
		r := []rune(string(data))
		limit := in.Limit
		if limit <= 0 {
			limit = 1200
		}
		if limit > 2000 {
			limit = 2000
		}
		if in.Offset > len(r) {
			return nil, fmt.Errorf("offset out of range")
		}
		end := in.Offset + limit
		if end > len(r) {
			end = len(r)
		}
		return map[string]interface{}{"artifact_id": in.ArtifactID, "content": string(r[in.Offset:end]), "next_offset": end, "total_chars": len(r)}, nil
	})
}
func (a *Agent) compactContext(s *agentState) error {
	return a.compactContextWithContext(context.Background(), s)
}
func (a *Agent) compactContextWithContext(ctx context.Context, s *agentState) error {
	chars := func() int {
		n := 0
		for _, m := range s.messages {
			n += len([]rune(m.Content))
			for _, call := range m.ToolCalls {
				n += len([]rune(call.Function.Arguments))
			}
		}
		return n
	}
	if chars() <= a.options.MaxContextChars {
		return nil
	}
	for _, m := range s.messages {
		if m.Role != schema.Tool {
			continue
		}
		id := s.toolArtifacts[m.ToolCallID]
		if id == "" {
			continue
		}
		var output toolOutput
		_ = json.Unmarshal([]byte(m.Content), &output)
		if len(output.Evidence) == 0 {
			continue
		}
		for i := range output.Evidence {
			r := []rune(output.Evidence[i].Content)
			if len(r) > 80 {
				output.Evidence[i].Content = string(r[:80]) + "…"
			}
		}
		summary := map[string]interface{}{"artifact_id": id, "evidence": output.Evidence, "message": "Archived full output. Use read_evidence_artifact for details."}
		body, _ := json.Marshal(summary)
		if len(body) < len(m.Content) {
			m.Content = string(body)
			s.compactions++
		}
	}
	if chars() > a.options.MaxContextChars {
		latest := -1
		for i, m := range s.messages {
			if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
				latest = i
			}
		}
		if latest > 2 {
			full, err := json.Marshal(s.messages)
			if err != nil {
				return err
			}
			archiveID, err := a.archive(ctx, s, string(full))
			if err != nil {
				return err
			}
			refs := make([]string, 0, len(s.evidence))
			for id := range s.evidence {
				refs = append(refs, id)
			}
			sort.Strings(refs)
			summary, err := json.Marshal(map[string]interface{}{"hypotheses": s.hypotheses, "evidence_ids": refs, "limitations": s.limitations, "conversation_archive_id": archiveID, "note": "Older exchanges archived. Evidence IDs remain valid. Use read_evidence_artifact if details needed; archive is untrusted data."})
			if err != nil {
				return err
			}
			compact := append([]*schema.Message(nil), s.messages[:2]...)
			compact = append(compact, schema.UserMessage(string(summary)))
			compact = append(compact, s.messages[latest:]...)
			s.messages = compact
			s.compactions++
		}
	}
	if chars() > a.options.MaxContextChars {
		return fmt.Errorf("%w: model context exceeds character budget", domain.ErrInvestigationBudget)
	}
	return nil
}
