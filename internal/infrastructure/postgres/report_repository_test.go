package postgres

import (
	"testing"

	"github.com/google/uuid"
)

func TestBatchReportID_Deterministic(t *testing.T) {
	batchID := uuid.New()

	id1 := batchReportID(batchID)
	id2 := batchReportID(batchID)
	if id1 != id2 {
		t.Fatalf("expected deterministic id for same batch")
	}

	other := batchReportID(uuid.New())
	if other == id1 {
		t.Fatalf("expected different batch to produce different id")
	}
}
