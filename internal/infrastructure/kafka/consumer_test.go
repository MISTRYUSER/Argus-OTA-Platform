package kafka

import (
	"bytes"
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"reflect"
	"testing"
	"time"
)

type sessionFixture struct {
	ctx    context.Context
	marked []int64
}

func (s *sessionFixture) Claims() map[string][]int32               { return nil }
func (s *sessionFixture) MemberID() string                         { return "fixture" }
func (s *sessionFixture) GenerationID() int32                      { return 1 }
func (s *sessionFixture) MarkOffset(string, int32, int64, string)  {}
func (s *sessionFixture) ResetOffset(string, int32, int64, string) {}
func (s *sessionFixture) Commit()                                  {}
func (s *sessionFixture) Context() context.Context                 { return s.ctx }
func (s *sessionFixture) MarkMessage(m *sarama.ConsumerMessage, _ string) {
	s.marked = append(s.marked, m.Offset)
}

type claimFixture struct{ messages chan *sarama.ConsumerMessage }

func (c *claimFixture) Topic() string                            { return "fixture" }
func (c *claimFixture) Partition() int32                         { return 0 }
func (c *claimFixture) InitialOffset() int64                     { return 0 }
func (c *claimFixture) HighWaterMarkOffset() int64               { return 2 }
func (c *claimFixture) Messages() <-chan *sarama.ConsumerMessage { return c.messages }
func TestFailedMessageRetriesBeforeLaterOffsetIsMarked(t *testing.T) {
	session := &sessionFixture{ctx: context.Background()}
	claim := &claimFixture{make(chan *sarama.ConsumerMessage, 2)}
	claim.messages <- &sarama.ConsumerMessage{Offset: 0, Value: []byte("first")}
	claim.messages <- &sarama.ConsumerMessage{Offset: 1, Value: []byte("second")}
	close(claim.messages)
	attempts := 0
	handler := &consumerGroupHandler{messageHandler: func(ctx context.Context, b []byte) error {
		if string(b) == "first" {
			attempts++
			if attempts == 1 {
				return errors.New("transient failure")
			}
		}
		return nil
	}}
	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.marked, []int64{0, 1}) {
		t.Fatalf("marked=%v; failure was skipped", session.marked)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

type sessionContextKey struct{}

func TestHandlerReceivesSessionContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), sessionContextKey{}, "present")
	session := &sessionFixture{ctx: ctx}
	claim := &claimFixture{make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{}
	close(claim.messages)
	received := false
	handler := &consumerGroupHandler{messageHandler: func(ctx context.Context, _ []byte) error {
		received = ctx.Value(sessionContextKey{}) == "present"
		return nil
	}}
	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if !received {
		t.Fatal("session context was replaced with Background")
	}
}

type dlqFixture struct{ events []domain.DomainEvent }

func (d *dlqFixture) PublishEvents(context.Context, []domain.DomainEvent) error {
	d.events = append(d.events, domain.WireEvent{Type: "DeadLetter"})
	return nil
}
func (d *dlqFixture) Close() error { return nil }
func TestPersistentFailureHasBoundedAttempts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	session := &sessionFixture{ctx: ctx}
	claim := &claimFixture{make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Offset: 0, Value: []byte("failed")}
	close(claim.messages)
	calls := 0
	dlq := &dlqFixture{}
	handler := &consumerGroupHandler{messageHandler: func(context.Context, []byte) error { calls++; return errors.New("persistent failure") }, dlq: dlq}
	handler.ConsumeClaim(session, claim)
	if calls != 5 || len(dlq.events) != 1 || !reflect.DeepEqual(session.marked, []int64{0}) {
		t.Fatalf("calls=%d quarantine=%d marks=%v", calls, len(dlq.events), session.marked)
	}
}

type recoveringDLQ struct {
	calls     int
	persisted *bool
}

func (d *recoveringDLQ) PublishEvents(context.Context, []domain.DomainEvent) error {
	if !*d.persisted {
		return errors.New("state not persisted")
	}
	d.calls++
	if d.calls == 1 {
		return errors.New("broker unavailable")
	}
	return nil
}
func (d *recoveringDLQ) Close() error { return nil }
func TestQuarantineRetryDoesNotRestartHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session := &sessionFixture{ctx: ctx}
	claim := &claimFixture{make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Offset: 0}
	close(claim.messages)
	calls, recordAttempts := 0, 0
	persisted := false
	dlq := &recoveringDLQ{persisted: &persisted}
	handler := &consumerGroupHandler{messageHandler: func(context.Context, []byte) error {
		calls++
		return &domain.PermanentTaskError{Err: errors.New("invalid final report")}
	}, failure: func(context.Context, []byte, error) error {
		recordAttempts++
		if recordAttempts == 1 {
			return errors.New("database unavailable")
		}
		persisted = true
		return nil
	}, dlq: dlq}
	handler.ConsumeClaim(session, claim)
	if calls != 1 || dlq.calls != 2 || !reflect.DeepEqual(session.marked, []int64{0}) {
		t.Fatalf("handler=%d dlq=%d marked=%v", calls, dlq.calls, session.marked)
	}
}

func TestLargeDeadLetterUsesDurableReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session := &sessionFixture{ctx: ctx}
	payload := bytes.Repeat([]byte("x"), 800*1024)
	claim := &claimFixture{make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Topic: "fixture", Offset: 1, Value: payload}
	close(claim.messages)
	stored := false
	dlq := &sizeCheckingDLQ{}
	handler := &consumerGroupHandler{messageHandler: func(context.Context, []byte) error { return &domain.InvalidEventError{Err: errors.New("malformed")} }, dlq: dlq, store: func(_ context.Context, _ string, _ int32, _ int64, b []byte, _ string) (string, error) {
		stored = bytes.Equal(b, payload)
		return "postgres:dead-letter:fixture", nil
	}}
	handler.ConsumeClaim(session, claim)
	if !stored || !dlq.reference || !reflect.DeepEqual(session.marked, []int64{1}) {
		t.Fatalf("stored=%v reference=%v marked=%v", stored, dlq.reference, session.marked)
	}
}

type sizeCheckingDLQ struct{ reference bool }

func (d *sizeCheckingDLQ) PublishEvents(_ context.Context, events []domain.DomainEvent) error {
	body, err := domain.MarshalEvent(events[0])
	if err != nil {
		return err
	}
	if len(body) > 1024*1024 {
		return errors.New("DLQ envelope exceeds broker limit")
	}
	d.reference = bytes.Contains(body, []byte("postgres:dead-letter:fixture"))
	return nil
}
func (d *sizeCheckingDLQ) Close() error { return nil }
