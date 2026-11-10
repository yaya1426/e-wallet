package identity

import (
	"sync"
	"time"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
)

// Login rate limits (TDR-011, T004): every attempt counts, successful or not.
const (
	loginLimitPerPhone = 10
	loginLimitPerIP    = 30
	loginWindow        = time.Minute
)

// fixedWindowLimiter allows limit hits per key in a fixed window that starts at the key's
// first hit. It is in memory, so limits are per process (phase 1).
type fixedWindowLimiter struct {
	limit  int
	window time.Duration
	clock  clock.Clock

	mu        sync.Mutex
	windows   map[string]*window
	lastPrune time.Time
}

type window struct {
	start time.Time
	hits  int
}

func newFixedWindowLimiter(limit int, w time.Duration, clk clock.Clock) *fixedWindowLimiter {
	return &fixedWindowLimiter{limit: limit, window: w, clock: clk, windows: map[string]*window{}}
}

// allow counts one hit for key and reports whether it is within the limit.
func (l *fixedWindowLimiter) allow(key string) bool {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.prune(now)
	w := l.windows[key]
	if w == nil || !now.Before(w.start.Add(l.window)) {
		w = &window{start: now}
		l.windows[key] = w
	}
	w.hits++
	return w.hits <= l.limit
}

// prune drops ended windows, at most once per window length, so memory stays bounded.
func (l *fixedWindowLimiter) prune(now time.Time) {
	if now.Sub(l.lastPrune) < l.window {
		return
	}
	for key, w := range l.windows {
		if !now.Before(w.start.Add(l.window)) {
			delete(l.windows, key)
		}
	}
	l.lastPrune = now
}

// LoginLimiter limits login attempts per phone and per client IP.
type LoginLimiter struct {
	perPhone *fixedWindowLimiter
	perIP    *fixedWindowLimiter
}

// NewLoginLimiter returns the limiter with the T004 limits: 10 per phone and 30 per IP per minute.
func NewLoginLimiter(clk clock.Clock) *LoginLimiter {
	return &LoginLimiter{
		perPhone: newFixedWindowLimiter(loginLimitPerPhone, loginWindow, clk),
		perIP:    newFixedWindowLimiter(loginLimitPerIP, loginWindow, clk),
	}
}

// Allow counts one attempt and reports whether it may proceed. The IP is checked first: an
// IP over its limit is refused without touching any phone counter, so it cannot lock other
// users out. Only well-formed phones get a counter, so attacker-chosen strings cannot grow
// the limiter's memory (review T004 #1); a malformed phone cannot match any user anyway.
func (l *LoginLimiter) Allow(phone, ip string) bool {
	if !l.perIP.allow("ip:" + ip) {
		return false
	}
	if !egyptianMobile.MatchString(phone) {
		return true
	}
	return l.perPhone.allow("phone:" + phone)
}

// registerWindow is the registration limit's window (TDR-018 §2).
const registerWindow = time.Hour

// RegisterLimiter slows mass account creation: at most perHour attempts per client IP per
// hour, valid or not (REGISTER_LIMIT_PER_HOUR, default 5). In memory, per instance.
type RegisterLimiter struct {
	perIP *fixedWindowLimiter
}

// NewRegisterLimiter returns the registration limiter.
func NewRegisterLimiter(perHour int, clk clock.Clock) *RegisterLimiter {
	return &RegisterLimiter{perIP: newFixedWindowLimiter(perHour, registerWindow, clk)}
}

// Allow reports whether one more registration attempt from ip is allowed, and counts it.
func (l *RegisterLimiter) Allow(ip string) bool {
	return l.perIP.allow("ip:" + ip)
}
