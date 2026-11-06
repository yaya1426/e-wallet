package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yaya1426/e-wallet/internal/platform/clock/clocktest"
)

func newFakeClock() *clocktest.Fake {
	return clocktest.New(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
}

func TestRateLimiter_PerPhone(t *testing.T) {
	l := NewLoginLimiter(newFakeClock())
	for i := range loginLimitPerPhone {
		assert.True(t, l.Allow("+201001234567", "10.0.0.1"), "attempt %d", i+1)
	}
	assert.False(t, l.Allow("+201001234567", "10.0.0.1"), "11th attempt for the phone")
	assert.True(t, l.Allow("+201001234568", "10.0.0.1"), "another phone is not affected")
}

func TestRateLimiter_PerIP(t *testing.T) {
	l := NewLoginLimiter(newFakeClock())
	for i := range loginLimitPerIP {
		phone := "+20100123" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "00"
		assert.True(t, l.Allow(phone, "10.0.0.1"), "attempt %d", i+1)
	}
	assert.False(t, l.Allow("+201009999999", "10.0.0.1"), "31st attempt from the IP")
	assert.True(t, l.Allow("+201009999999", "10.0.0.2"), "another IP is not affected")
}

func TestRateLimiter_WindowReset(t *testing.T) {
	clk := newFakeClock()
	l := NewLoginLimiter(clk)
	for range loginLimitPerPhone + 1 {
		l.Allow("+201001234567", "10.0.0.1")
	}
	assert.False(t, l.Allow("+201001234567", "10.0.0.1"))

	clk.Advance(loginWindow - time.Second)
	assert.False(t, l.Allow("+201001234567", "10.0.0.1"), "still inside the window")

	clk.Advance(time.Second)
	assert.True(t, l.Allow("+201001234567", "10.0.0.1"), "a new window starts")
}

func TestRateLimiter_PrunesEndedWindows(t *testing.T) {
	clk := newFakeClock()
	l := newFixedWindowLimiter(1, time.Minute, clk)
	for _, key := range []string{"a", "b", "c"} {
		l.allow(key)
	}
	clk.Advance(2 * time.Minute)
	l.allow("d")

	assert.Len(t, l.windows, 1, "only the live window is kept")
}

func TestLoginLimiter_BlockedIPDoesNotCountPhones(t *testing.T) {
	l := NewLoginLimiter(newFakeClock())
	for range loginLimitPerIP {
		l.Allow("+201009999999", "203.0.113.1") // burn the attacker IP's budget
	}
	victim := "+201001234567"
	for range 50 {
		assert.False(t, l.Allow(victim, "203.0.113.1"), "blocked IP")
	}
	assert.True(t, l.Allow(victim, "198.51.100.7"), "the victim can still log in from their own IP")
}

func TestLoginLimiter_MalformedPhonesAreNotStored(t *testing.T) {
	l := NewLoginLimiter(newFakeClock())
	for i := range 20 {
		assert.True(t, l.Allow(strings.Repeat("x", 1000)+string(rune('a'+i)), "10.0.0.1"))
	}
	assert.Empty(t, l.perPhone.windows, "no counter for strings that are not phones")
}
