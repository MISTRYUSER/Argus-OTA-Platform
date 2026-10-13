package domain

import (
	"context"
	"time"
)

// IncidentScope is resolved from trusted storage, never from model arguments.
type IncidentScope struct {
	Namespace string
	Service   string
	Start     time.Time
	End       time.Time
}
type ScopeResolver func(context.Context, string) (IncidentScope, error)
type MetricQuery struct {
	Metric string `json:"metric"`
	Limit  int    `json:"limit,omitempty"`
}
type LogQuery struct {
	Contains string `json:"contains,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}
type TraceQuery struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type TelemetryReader interface {
	QueryMetrics(context.Context, string, MetricQuery) ([]EvidenceItem, error)
	QueryLogs(context.Context, string, LogQuery) ([]EvidenceItem, error)
	QueryTraces(context.Context, string, TraceQuery) ([]EvidenceItem, error)
}
