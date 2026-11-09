package queue

import (
	"math/rand/v2"
	"time"
)

// Backoff bounds (TDR-006 §3).
const (
	backoffBase = time.Second
	backoffCap  = 5 * time.Minute
)

// Backoff returns the wait before retrying a job that has been attempted `attempt` times:
// full jitter, a random duration in [0, min(5m, 1s·2^attempt)). rnd(n) must return a value in
// [0, n); nil means math/rand/v2.
func Backoff(attempt int, rnd func(n int64) int64) time.Duration {
	if rnd == nil {
		rnd = rand.Int64N // jitter, not security
	}
	ceiling := backoffCap
	if attempt < 30 { // 2^30 s is far above the cap; avoid shifting into overflow
		if exp := backoffBase << max(attempt, 0); exp < backoffCap {
			ceiling = exp
		}
	}
	return time.Duration(rnd(int64(ceiling)))
}
