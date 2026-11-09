package queue

// SetJitter replaces the backoff jitter source, for tests.
func SetJitter(r *Runner, rnd func(n int64) int64) { r.rnd = rnd }
