package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(nil, clocktest.New(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)),
		Config{Workers: 1, MaxAttempts: 3, PollInterval: time.Second}, nil, nil)
	require.NoError(t, err)
	return s
}

func done(context.Context, queue.Job) queue.Result { return queue.Done() }

func TestEnqueue_UnknownType(t *testing.T) {
	s := newTestService(t)
	s.Register("known.type", done)

	// The type is checked before anything is written, so no transaction is needed.
	err := s.Enqueue(context.Background(), nil, Event{AggregateType: "transaction", AggregateID: uuid.New(), Type: "unknown.type"})

	require.ErrorIs(t, err, ErrUnknownEventType)
	assert.Contains(t, err.Error(), `"unknown.type"`)
}

func TestEnqueue_PayloadNotEncodable(t *testing.T) {
	s := newTestService(t)
	s.Register("known.type", done)

	err := s.Enqueue(context.Background(), nil, Event{Type: "known.type", Payload: make(chan int)})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode")
}

func TestRegister_WiringBugsPanic(t *testing.T) {
	s := newTestService(t)
	s.Register("a.type", done)

	assert.PanicsWithValue(t, `outbox: event type "a.type" registered twice`, func() { s.Register("a.type", done) })
	assert.PanicsWithValue(t, `outbox: Register("b.type") with a nil handler`, func() { s.Register("b.type", nil) })
}

func TestNewService_InvalidConfig(t *testing.T) {
	_, err := NewService(nil, clocktest.New(time.Now()), Config{Workers: 0, MaxAttempts: 0, PollInterval: 0}, nil, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "workers")
	assert.Contains(t, err.Error(), "max attempts")
	assert.Contains(t, err.Error(), "poll interval")
}

func TestNewService_DefaultLeaseAndTimeout(t *testing.T) {
	// A zero Lease or HandlerTimeout would be rejected by the queue engine, so the defaults
	// must be filled in.
	_, err := NewService(nil, clocktest.New(time.Now()), Config{Workers: 1, MaxAttempts: 1, PollInterval: time.Second}, nil, nil)
	require.NoError(t, err)
}

func TestDeliver_CallsRegisteredHandler(t *testing.T) {
	s := newTestService(t)
	var got queue.Job
	s.Register("a.type", func(_ context.Context, job queue.Job) queue.Result {
		got = job
		return queue.Fail("REJECTED")
	})
	job := queue.Job{ID: uuid.New(), Type: "a.type", Payload: []byte(`{"x":1}`), Attempts: 1}

	res := s.deliver(context.Background(), job)

	assert.Equal(t, queue.Fail("REJECTED"), res)
	assert.Equal(t, job, got)
}
