package llm

import (
	"fmt"
	sdk "github.com/meguminnnnnnnnn/go-openai"
	"testing"
)

func TestPermanentAPIStatusClassification(t *testing.T) {
	for _, test := range []struct {
		status int
		want   bool
	}{{400, true}, {401, true}, {402, true}, {403, true}, {404, true}, {422, true}, {429, false}, {500, false}, {503, false}} {
		err := fmt.Errorf("wrapped: %w", &sdk.APIError{HTTPStatusCode: test.status})
		if got := IsPermanentError(err); got != test.want {
			t.Fatalf("status=%d got=%v", test.status, got)
		}
	}
}
