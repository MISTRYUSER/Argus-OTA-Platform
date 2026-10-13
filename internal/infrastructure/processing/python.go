// Package processing connects actual uploaded objects to bounded Python analysis.
package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const MaxBytes = 32 * 1024 * 1024

type Python struct{ Executable, Script, Baseline, Candidate, RecordMetadata string }
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("processing output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (p Python) run(ctx context.Context, input []byte, args ...string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) > MaxBytes {
		return nil, fmt.Errorf("processing input exceeds limit")
	}
	exe := p.Executable
	if exe == "" {
		exe = "python3"
	}
	script := p.Script
	if script == "" {
		script = "workers/python-aggregator/regression.py"
	}
	// All process arguments are explicit; no shell or input-selected executable.
	child, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(child, exe, append([]string{script}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	out := &boundedBuffer{limit: MaxBytes}
	stderr := &boundedBuffer{limit: 8192}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("python processing: %w: %s", err, stderr.String())
	}
	if !json.Valid(out.Bytes()) {
		return nil, fmt.Errorf("invalid processing JSON")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}
func (p Python) Parse(ctx context.Context, path, source string) (json.RawMessage, error) {
	args := []string{"parse", "--file", path, "--source", source}
	if p.RecordMetadata != "" {
		args = append(args, "--metadata-json", p.RecordMetadata)
	}
	return p.run(ctx, nil, args...)
}
func (p Python) Analyze(ctx context.Context, rows []byte) (json.RawMessage, error) {
	args := []string{"analyze"}
	if p.Baseline != "" {
		args = append(args, "--baseline", p.Baseline)
	}
	if p.Candidate != "" {
		args = append(args, "--candidate", p.Candidate)
	}
	return p.run(ctx, rows, args...)
}
