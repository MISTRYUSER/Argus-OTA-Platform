package pgvector

import "testing"

func TestToVectorLiteral(t *testing.T) {
	_, err := toVectorLiteral(nil)
	if err == nil {
		t.Fatalf("expected error for empty vector")
	}

	lit, err := toVectorLiteral([]float64{0.1, -2.5, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "[0.1,-2.5,3]"
	if lit != want {
		t.Fatalf("unexpected literal, got %s, want %s", lit, want)
	}
}

func TestBuildErrorCodesParam(t *testing.T) {
	if p := buildErrorCodesParam(nil); p != nil {
		t.Fatalf("expected nil param for nil error codes")
	}

	if p := buildErrorCodesParam([]string{}); p != nil {
		t.Fatalf("expected nil param for empty error codes")
	}

	if p := buildErrorCodesParam([]string{"E001"}); p == nil {
		t.Fatalf("expected non-nil param for non-empty error codes")
	}
}
