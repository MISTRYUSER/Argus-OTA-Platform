// Package telemetryquery implements bounded, incident-scoped read access to open-source telemetry backends.
package telemetryquery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

const hardBytes int64 = 1 << 20
const maxResults = 100
const normalizedBytes = 16 << 10
const contentRunes = 1000

var scopeLabel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$`)
var metricName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

type Config struct {
	PrometheusURL, LokiURL, TempoURL string
	AllowedMetrics                   []string
	Timeout                          time.Duration
	MaxResponseBytes                 int64
}
type Client struct {
	cfg     Config
	resolve domain.ScopeResolver
	http    *http.Client
	metrics map[string]bool
}

var _ domain.TelemetryReader = (*Client)(nil)

func NewClient(cfg Config, resolve domain.ScopeResolver) (*Client, error) {
	if resolve == nil {
		return nil, errors.New("trusted incident scope resolver required")
	}
	for _, endpoint := range []string{cfg.PrometheusURL, cfg.LokiURL, cfg.TempoURL} {
		u, e := url.Parse(endpoint)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(endpoint) > 512 {
			return nil, errors.New("invalid trusted telemetry endpoint")
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Timeout > 30*time.Second {
		return nil, errors.New("telemetry timeout exceeds 30 seconds")
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = hardBytes
	}
	if cfg.MaxResponseBytes > hardBytes {
		return nil, errors.New("telemetry response cap exceeds 1 MiB")
	}
	c := &Client{cfg: cfg, resolve: resolve, metrics: map[string]bool{}, http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("telemetry redirects forbidden") }}}
	for _, m := range cfg.AllowedMetrics {
		if !metricName.MatchString(m) || len(m) > 128 {
			return nil, errors.New("invalid metric allowlist")
		}
		c.metrics[m] = true
	}
	return c, nil
}
func validateScope(s domain.IncidentScope) error {
	if !scopeLabel.MatchString(s.Namespace) || !scopeLabel.MatchString(s.Service) || s.Start.IsZero() || !s.End.After(s.Start) || s.End.Sub(s.Start) > time.Hour || s.Start.Unix() < 0 || s.End.After(time.Unix(0, 1<<63-1)) {
		return errors.New("invalid incident scope")
	}
	return nil
}
func (c *Client) scope(ctx context.Context, batch string) (domain.IncidentScope, error) {
	if err := ctx.Err(); err != nil {
		return domain.IncidentScope{}, err
	}
	if strings.TrimSpace(batch) == "" || len(batch) > 128 {
		return domain.IncidentScope{}, errors.New("invalid batch ID")
	}
	s, e := c.resolve(ctx, batch)
	if e != nil {
		return s, e
	}
	return s, validateScope(s)
}
func limit(n int) (int, error) {
	if n == 0 {
		return 20, nil
	}
	if n < 1 || n > maxResults {
		return 0, errors.New("query limit must be 1..100")
	}
	return n, nil
}
func labels(s domain.IncidentScope) string {
	return `namespace=` + strconv.Quote(s.Namespace) + `,service_name=` + strconv.Quote(s.Service)
}
func (c *Client) get(ctx context.Context, endpoint, path string, q url.Values, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+path+"?"+q.Encode(), nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	res, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry HTTP status %d", res.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, c.cfg.MaxResponseBytes+1))
	if e != nil {
		return e
	}
	if int64(len(body)) > c.cfg.MaxResponseBytes {
		return errors.New("telemetry response exceeds byte limit")
	}
	if e = json.Unmarshal(body, out); e != nil {
		return fmt.Errorf("invalid telemetry JSON: %w", e)
	}
	return nil
}

// evidence hashes the original scoped record before normalizing or truncating its display.
func evidence(kind, batch, endpoint, query string, s domain.IncidentScope, original, normalized any) domain.EvidenceItem {
	raw, _ := json.Marshal(original)
	content, _ := json.Marshal(normalized)
	originalHash := sha256.Sum256(raw)
	provenance := fmt.Sprintf("%s endpoint=%s batch=%s namespace=%s service=%s start=%s end=%s query=%s original_sha256=%x", kind, endpoint, batch, s.Namespace, s.Service, s.Start.UTC().Format(time.RFC3339Nano), s.End.UTC().Format(time.RFC3339Nano), query, originalHash)
	h := sha256.Sum256([]byte(provenance + "\x00" + string(raw)))
	display := string(content)
	if utf8.RuneCountInString(display) > contentRunes {
		marker := fmt.Sprintf("…[truncated original_sha256=%x]", originalHash)
		display = string([]rune(display)[:contentRunes-utf8.RuneCountInString(marker)]) + marker
		provenance += " content_truncated=true"
	}
	return domain.EvidenceItem{ID: kind + ":" + hex.EncodeToString(h[:]), Source: provenance, Content: display}
}

// boundedEvidence accounts for JSON escaping and metadata, including the explicit budget marker.
func boundedEvidence(items []domain.EvidenceItem) []domain.EvidenceItem {
	for count := len(items); count > 0; count-- {
		selected := append([]domain.EvidenceItem(nil), items[:count]...)
		if count < len(items) {
			selected[count-1].Source += fmt.Sprintf(" result_truncated=true returned=%d available=%d", count, len(items))
		}
		encoded, _ := json.Marshal(selected)
		if len(encoded) <= normalizedBytes {
			return selected
		}
	}
	return []domain.EvidenceItem{}
}
func seconds(t time.Time) string {
	if t.Nanosecond() == 0 {
		return strconv.FormatInt(t.Unix(), 10)
	}
	return fmt.Sprintf("%d.%s", t.Unix(), strings.TrimRight(fmt.Sprintf("%09d", t.Nanosecond()), "0"))
}
func withinMetricWindow(stamp json.RawMessage, s domain.IncidentScope) bool {
	if len(stamp) > 64 {
		return false
	}
	value, ok := new(big.Rat).SetString(string(stamp))
	if !ok {
		return false
	}
	start := new(big.Rat).SetFrac(big.NewInt(s.Start.UnixNano()), big.NewInt(1e9))
	end := new(big.Rat).SetFrac(big.NewInt(s.End.UnixNano()), big.NewInt(1e9))
	return value.Cmp(start) >= 0 && value.Cmp(end) <= 0
}
func traceScopeLabel(labels map[string]string, key, want string) bool {
	short, shortOK := labels[key]
	qualified, qualifiedOK := labels["resource."+key]
	if shortOK && qualifiedOK && short != qualified {
		return false
	}
	return (shortOK && short == want) || (qualifiedOK && qualified == want)
}
func matching(m map[string]string, s domain.IncidentScope) bool {
	return m["namespace"] == s.Namespace && m["service_name"] == s.Service
}
func (c *Client) QueryMetrics(ctx context.Context, batch string, in domain.MetricQuery) ([]domain.EvidenceItem, error) {
	if !c.metrics[in.Metric] {
		return nil, errors.New("metric not allowlisted")
	}
	n, e := limit(in.Limit)
	if e != nil {
		return nil, e
	}
	s, e := c.scope(ctx, batch)
	if e != nil {
		return nil, e
	}
	// Prometheus evaluates millisecond timestamps. Round inward so its wire precision
	// cannot move a boundary sample outside the immutable incident window.
	requestStart := s.Start.Truncate(time.Millisecond)
	if requestStart.Before(s.Start) {
		requestStart = requestStart.Add(time.Millisecond)
	}
	requestEnd := s.End.Truncate(time.Millisecond)
	if requestEnd.Before(requestStart) {
		return []domain.EvidenceItem{}, nil
	}
	query := in.Metric + "{" + labels(s) + "}"
	step := int64(s.End.Sub(s.Start).Seconds()) / int64(n)
	if step < 1 {
		step = 1
	}
	params := url.Values{"query": {query}, "start": {seconds(requestStart)}, "end": {seconds(requestEnd)}, "step": {strconv.FormatInt(step, 10)}}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string   `json:"metric"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if e = c.get(ctx, c.cfg.PrometheusURL, "/api/v1/query_range", params, &out); e != nil {
		return nil, e
	}
	if out.Status != "success" || out.Data.ResultType != "matrix" {
		return nil, errors.New("unexpected metrics response")
	}
	result := []domain.EvidenceItem{}
	for _, series := range out.Data.Result {
		if !matching(series.Metric, s) {
			return nil, errors.New("metrics response outside incident scope")
		}
		for _, v := range series.Values {
			if len(v) != 2 {
				return nil, errors.New("invalid metric sample")
			}
			var value string
			if !withinMetricWindow(v[0], s) || json.Unmarshal(v[1], &value) != nil || len(value) > 64 {
				return nil, errors.New("metric sample outside incident window")
			}
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, errors.New("invalid metric value")
			}
			if len(result) < n {
				result = append(result, evidence("prometheus", batch, c.cfg.PrometheusURL, query, s, map[string]any{"metric": series.Metric, "sample": v}, map[string]any{"metric": in.Metric, "namespace": s.Namespace, "service": s.Service, "timestamp": json.RawMessage(v[0]), "value": value}))
			}
		}
	}
	return boundedEvidence(result), nil
}
func (c *Client) QueryLogs(ctx context.Context, batch string, in domain.LogQuery) ([]domain.EvidenceItem, error) {
	if len(in.Contains) > 256 {
		return nil, errors.New("log search exceeds 256 bytes")
	}
	n, e := limit(in.Limit)
	if e != nil {
		return nil, e
	}
	s, e := c.scope(ctx, batch)
	if e != nil {
		return nil, e
	}
	query := "{" + labels(s) + "}"
	if in.Contains != "" {
		query += " |= " + strconv.Quote(in.Contains)
	}
	params := url.Values{"query": {query}, "start": {strconv.FormatInt(s.Start.UnixNano(), 10)}, "end": {strconv.FormatInt(s.End.UnixNano(), 10)}, "limit": {strconv.Itoa(n)}, "direction": {"forward"}}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if e = c.get(ctx, c.cfg.LokiURL, "/loki/api/v1/query_range", params, &out); e != nil {
		return nil, e
	}
	if out.Status != "success" || out.Data.ResultType != "streams" {
		return nil, errors.New("unexpected logs response")
	}
	result := []domain.EvidenceItem{}
	for _, stream := range out.Data.Result {
		if !matching(stream.Stream, s) {
			return nil, errors.New("logs response outside incident scope")
		}
		for _, v := range stream.Values {
			if len(v) != 2 {
				return nil, errors.New("invalid log entry")
			}
			stamp, e := strconv.ParseInt(v[0], 10, 64)
			if e != nil || stamp < s.Start.UnixNano() || stamp > s.End.UnixNano() {
				return nil, errors.New("log entry outside incident window")
			}
			if len(result) < n {
				result = append(result, evidence("loki", batch, c.cfg.LokiURL, query, s, map[string]any{"labels": stream.Stream, "timestamp": v[0], "line": v[1]}, map[string]any{"namespace": s.Namespace, "service": s.Service, "timestamp": v[0], "line": v[1]}))
			}
		}
	}
	return boundedEvidence(result), nil
}
func (c *Client) QueryTraces(ctx context.Context, batch string, in domain.TraceQuery) ([]domain.EvidenceItem, error) {
	if in.Status != "" && in.Status != "error" && in.Status != "ok" && in.Status != "unset" {
		return nil, errors.New("invalid trace status")
	}
	n, e := limit(in.Limit)
	if e != nil {
		return nil, e
	}
	s, e := c.scope(ctx, batch)
	if e != nil {
		return nil, e
	}
	query := `{ resource.k8s.namespace.name = ` + strconv.Quote(s.Namespace) + ` && resource.service.name = ` + strconv.Quote(s.Service)
	if in.Status != "" {
		query += " && status = " + in.Status
	}
	query += " } | select(resource.k8s.namespace.name, resource.service.name)"
	params := url.Values{"q": {query}, "start": {strconv.FormatInt(s.Start.Unix(), 10)}, "end": {strconv.FormatInt(s.End.Unix(), 10)}, "limit": {strconv.Itoa(n)}, "spss": {"1"}}
	var out struct {
		Traces []json.RawMessage `json:"traces"`
	}
	if e = c.get(ctx, c.cfg.TempoURL, "/api/search", params, &out); e != nil {
		return nil, e
	}
	if out.Traces == nil {
		return nil, errors.New("missing trace search result")
	}
	result := []domain.EvidenceItem{}
	for _, raw := range out.Traces {
		var trace struct {
			TraceID  string `json:"traceID"`
			SpanSets []struct {
				Spans []struct {
					SpanID     string `json:"spanID"`
					Name       string `json:"name"`
					Start      string `json:"startTimeUnixNano"`
					Duration   string `json:"durationNanos"`
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							String string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"spans"`
			} `json:"spanSets"`
		}
		if json.Unmarshal(raw, &trace) != nil || trace.TraceID == "" || len(trace.SpanSets) == 0 {
			return nil, errors.New("malformed trace spans")
		}
		for _, set := range trace.SpanSets {
			if len(set.Spans) == 0 {
				return nil, errors.New("missing matched trace spans")
			}
			for _, span := range set.Spans {
				labels := map[string]string{}
				for _, a := range span.Attributes {
					if prior, exists := labels[a.Key]; exists && prior != a.Value.String {
						return nil, errors.New("conflicting trace attribute")
					}
					labels[a.Key] = a.Value.String
				}
				if !traceScopeLabel(labels, "k8s.namespace.name", s.Namespace) || !traceScopeLabel(labels, "service.name", s.Service) {
					return nil, errors.New("trace span outside incident scope")
				}
				stamp, err := strconv.ParseInt(span.Start, 10, 64)
				if err != nil || stamp < s.Start.UnixNano() || stamp > s.End.UnixNano() || span.SpanID == "" {
					return nil, errors.New("invalid trace span time or ID")
				}
				if len(result) < n {
					result = append(result, evidence("tempo", batch, c.cfg.TempoURL, query, s, map[string]any{"trace_id": trace.TraceID, "span": span}, map[string]any{"trace_id": trace.TraceID, "span_id": span.SpanID, "name": span.Name, "start_time_unix_nano": span.Start, "duration_nanos": span.Duration, "namespace": s.Namespace, "service": s.Service}))
				}
			}
		}
	}
	return boundedEvidence(result), nil
}
