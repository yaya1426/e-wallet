package reconciliation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nop(context.Context) Result { return Result{} }

// AC-4.
func TestRegister_Validation(t *testing.T) {
	ok := Job{Name: "pending_bank_ops", Interval: time.Minute, Timeout: time.Minute, Run: nop}
	bad := []struct {
		name string
		job  Job
	}{
		{"empty name", Job{Interval: time.Minute, Timeout: time.Second, Run: nop}},
		{"upper case name", Job{Name: "Pending", Interval: time.Minute, Timeout: time.Second, Run: nop}},
		{"name with dash", Job{Name: "pending-ops", Interval: time.Minute, Timeout: time.Second, Run: nop}},
		{"zero interval", Job{Name: "a", Timeout: time.Second, Run: nop}},
		{"zero timeout", Job{Name: "a", Interval: time.Minute, Run: nop}},
		{"timeout above interval", Job{Name: "a", Interval: time.Second, Timeout: 2 * time.Second, Run: nop}},
		{"no run", Job{Name: "a", Interval: time.Minute, Timeout: time.Second}},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scheduler{jitter: jittered}
			assert.Panics(t, func() { s.Register(tt.job) })
			assert.Empty(t, s.Jobs())
		})
	}

	s := &Scheduler{jitter: jittered}
	s.Register(ok)
	assert.Equal(t, []string{"pending_bank_ops"}, s.Jobs())
	assert.Panics(t, func() { s.Register(ok) }, "duplicate")
	s.started.Store(true)
	assert.Panics(t, func() { s.Register(Job{Name: "later", Interval: time.Minute, Timeout: time.Second, Run: nop}) }, "after Run")
}

// AC-8: computed independently with Python hashlib.
func TestLockKey_Stable(t *testing.T) {
	assert.Equal(t, int64(-5841883978041386783), LockKey("pending_bank_ops"))
	assert.Equal(t, int64(-1715456754475748426), LockKey("dead_outbox"))
	assert.NotEqual(t, LockKey("a"), LockKey("b"))
}

func TestJittered_WithinTenPercent(t *testing.T) {
	for range 1000 {
		d := jittered(time.Minute)
		require.GreaterOrEqual(t, d, 54*time.Second)
		require.LessOrEqual(t, d, 66*time.Second)
	}
	assert.Equal(t, time.Duration(3), jittered(3), "too small to spread")
}

func TestUnknownJob(t *testing.T) {
	s := &Scheduler{jitter: jittered}
	_, _, err := s.RunOnce(context.Background(), "nope")
	require.ErrorIs(t, err, ErrUnknownJob)
}
