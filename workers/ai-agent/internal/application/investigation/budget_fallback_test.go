package investigation

import (
	"context"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"sync/atomic"
	"testing"
)

type bindCounter struct {
	model.ChatModel
	binds atomic.Int32
}

func (b *bindCounter) BindTools([]*schema.ToolInfo) error { b.binds.Add(1); return nil }
func TestBudgetFallbackDoesNotRebindSharedModel(t *testing.T) {
	chat := &bindCounter{}
	a, err := NewAgent(repositoryFixture{}, nil, chat, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.RunWithBudget(context.Background(), uuid.NewString(), 1)
	if chat.binds.Load() != 1 {
		t.Fatalf("shared model rebound %d times", chat.binds.Load())
	}
	if a.options.MaxReservedTokens != 1000000 {
		t.Fatal("shared agent budget mutated")
	}
}
