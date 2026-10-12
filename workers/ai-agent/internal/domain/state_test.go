package domain

import "testing"

func TestRecordWorkerIdentifiesCompletedWorker(t *testing.T) {
	s := NewDiagnosisContext("fixture")
	s.RecordWorker("guard")
	if s.LastWorker != "guard" {
		t.Fatalf("last completed worker=%q", s.LastWorker)
	}
}
