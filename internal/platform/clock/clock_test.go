package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

func utc(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

func TestSystem_UTC(t *testing.T) {
	var c clock.Clock = clock.System{}

	now := c.Now()

	assert.Equal(t, time.UTC, now.Location())
	assert.WithinDuration(t, time.Now(), now, time.Second)
}

func TestFake_SetAdvance(t *testing.T) {
	start := utc(t, "2026-11-05T10:00:00Z")
	var c clock.Clock = clocktest.New(start.In(clock.Cairo))
	f := c.(*clocktest.Fake)

	assert.Equal(t, start, f.Now())
	assert.Equal(t, time.UTC, f.Now().Location())

	f.Advance(90 * time.Minute)
	assert.Equal(t, utc(t, "2026-11-05T11:30:00Z"), f.Now())

	f.Set(utc(t, "2027-01-01T00:00:00Z").In(clock.Cairo))
	assert.Equal(t, utc(t, "2027-01-01T00:00:00Z"), f.Now())
	assert.Equal(t, time.UTC, f.Now().Location())
}

func TestFake_Concurrent(t *testing.T) {
	f := clocktest.New(utc(t, "2026-11-05T10:00:00Z"))
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); f.Advance(time.Second) }()
		go func() { defer wg.Done(); _ = f.Now() }()
	}
	wg.Wait()

	assert.Equal(t, utc(t, "2026-11-05T10:00:50Z"), f.Now())
}

func TestCairoDay_Winter(t *testing.T) {
	start, end := clock.CairoDay(utc(t, "2026-11-05T10:00:00Z"))

	assert.Equal(t, utc(t, "2026-11-04T22:00:00Z"), start)
	assert.Equal(t, utc(t, "2026-11-05T22:00:00Z"), end)
	assert.Equal(t, time.UTC, start.Location())
	assert.Equal(t, time.UTC, end.Location())
}

func TestCairoDay_Boundary(t *testing.T) {
	before := utc(t, "2026-11-05T21:59:59Z")
	after := utc(t, "2026-11-05T22:00:00Z")

	s1, e1 := clock.CairoDay(before)
	s2, e2 := clock.CairoDay(after)

	assert.Equal(t, utc(t, "2026-11-04T22:00:00Z"), s1)
	assert.Equal(t, after, e1)
	assert.Equal(t, after, s2, "22:00Z is midnight in Cairo and starts the next day")
	assert.Equal(t, utc(t, "2026-11-06T22:00:00Z"), e2)

	// Every instant inside the interval maps back to the same day.
	s, e := clock.CairoDay(before)
	assert.Equal(t, s, mustDayStart(t, s))
	assert.Equal(t, s, mustDayStart(t, e.Add(-time.Second)))
}

func TestCairoDay_DSTStart2026(t *testing.T) {
	// Friday 24 April 2026: midnight does not exist, clocks go from 00:00 to 01:00.
	for _, in := range []string{"2026-04-23T22:00:00Z", "2026-04-24T10:00:00Z", "2026-04-24T20:59:59Z"} {
		start, end := clock.CairoDay(utc(t, in))

		assert.Equal(t, utc(t, "2026-04-23T22:00:00Z"), start, "input %s", in)
		assert.Equal(t, utc(t, "2026-04-24T21:00:00Z"), end, "input %s", in)
		assert.Equal(t, 23*time.Hour, end.Sub(start))
	}
	// The instant before the day starts belongs to the day before.
	_, prevEnd := clock.CairoDay(utc(t, "2026-04-23T21:59:59Z"))
	assert.Equal(t, utc(t, "2026-04-23T22:00:00Z"), prevEnd)
}

func TestCairoDay_DSTEnd2026(t *testing.T) {
	// Thursday 29 October 2026: at 24:00 clocks go back to 23:00, so the day lasts 25 hours.
	for _, in := range []string{"2026-10-28T21:00:00Z", "2026-10-29T12:00:00Z", "2026-10-29T21:30:00Z", "2026-10-29T21:59:59Z"} {
		start, end := clock.CairoDay(utc(t, in))

		assert.Equal(t, utc(t, "2026-10-28T21:00:00Z"), start, "input %s", in)
		assert.Equal(t, utc(t, "2026-10-29T22:00:00Z"), end, "input %s", in)
		assert.Equal(t, 25*time.Hour, end.Sub(start))
	}
}

func mustDayStart(t *testing.T, in time.Time) time.Time {
	t.Helper()
	s, _ := clock.CairoDay(in)
	return s
}
