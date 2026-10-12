package domain

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// WireEvent carries stage requests and durable payloads on the shared event bus.
type WireEvent struct {
	Type      string
	BatchID   uuid.UUID
	Timestamp time.Time
	Data      map[string]interface{}
	Raw       json.RawMessage
}

func (e WireEvent) OccurredOn() time.Time  { return e.Timestamp }
func (e WireEvent) AggregateID() uuid.UUID { return e.BatchID }
func (e WireEvent) EventType() string      { return e.Type }
func MarshalEvent(event DomainEvent) ([]byte, error) {
	if e, ok := event.(WireEvent); ok && len(e.Raw) > 0 {
		return e.Raw, nil
	}
	m := map[string]interface{}{"event_type": event.EventType(), "batch_id": event.AggregateID(), "timestamp": event.OccurredOn().Format(time.RFC3339Nano)}
	switch e := event.(type) {
	case BatchCreated:
		m["vehicle_id"] = e.VehicleID
		m["vin"] = e.VIN
	case BatchStatusChanged:
		m["old_status"] = e.OldStatus
		m["new_status"] = e.NewStatus
	case FileParsed:
		m["file_id"] = e.FileID
	case GatheringCompleted:
		m["version"] = e.Version
		m["total_files"] = e.TotalFiles
		m["chart_files"] = e.ChartFiles
	case DiagnosisCompleted:
		m["version"] = e.Version
		m["diagnosis_id"] = e.DiagnosisID
		m["diagnosis_summary"] = e.DiagnosisSummary
		m["top_error_codes"] = e.TopErrorCodes
		m["token_usage"] = e.TokenUsage
	case WireEvent:
		for k, v := range e.Data {
			m[k] = v
		}
	default:
		return nil, fmt.Errorf("unsupported domain event %T", event)
	}
	return json.Marshal(m)
}
func EventKey(e DomainEvent) string {
	key := e.EventType() + ":" + e.AggregateID().String()
	switch v := e.(type) {
	case BatchStatusChanged:
		key += ":" + v.NewStatus.String()
	case FileParsed:
		key += ":" + v.FileID.String()
	}
	return key
}
