package kafka

import (
	"reflect"
	"testing"
)

func TestIsGatheringCompletedEvent(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "GatheringCompleted", want: true},
		{in: "gathering-completed", want: true},
		{in: " gathering-completed ", want: true},
		{in: "diagnosis-completed", want: false},
	}

	for _, tt := range tests {
		got := isGatheringCompletedEvent(tt.in)
		if got != tt.want {
			t.Fatalf("input %q: got %v want %v", tt.in, got, tt.want)
		}
	}
}

func TestExtractTopKErrorCodes(t *testing.T) {
	stats := map[string]int{
		"E003": 2,
		"E001": 5,
		"E004": 3,
		"E002": 5,
		"E005": 1,
	}

	got := extractTopKErrorCodes(stats, 3)
	want := []string{"E001", "E002", "E004"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
