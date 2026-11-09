// Package queuetest provides an in-memory queue.Store for unit tests.
package queuetest

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

// Status of a fake job.
type Status string

// Fake job statuses (the same words the real tables use).
const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusDone       Status = "DONE"
	StatusDead       Status = "DEAD"
	StatusFailed     Status = "FAILED"
)

// Record is the stored state of one job.
type Record struct {
	Job         queue.Job
	Status      Status
	NextAt      time.Time
	LockedUntil time.Time
	LockedBy    string
	LastError   string
	Retries     []time.Time // next_attempt_at of each MarkRetry
}

// Store is a thread-safe in-memory queue.Store driven by a clock.
type Store struct {
	clock clock.Clock

	mu         sync.Mutex
	jobs       []*Record
	claims     int
	staleMarks int
	ClaimFn    func() error // optional: makes Claim fail
}

// New returns an empty store.
func New(clk clock.Clock) *Store { return &Store{clock: clk} }

// Add enqueues a job that is due now.
func (s *Store) Add(jobType string, payload []byte) uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New()
	s.jobs = append(s.jobs, &Record{Job: queue.Job{ID: id, Type: jobType, Payload: payload}, Status: StatusPending, NextAt: s.clock.Now()})
	return id
}

// Claim implements queue.Store.
func (s *Store) Claim(_ context.Context, owner string, n int, lease time.Duration) ([]queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.ClaimFn != nil {
		if err := s.ClaimFn(); err != nil {
			return nil, err
		}
	}
	now := s.clock.Now()
	var out []queue.Job
	for _, r := range s.jobs {
		if len(out) == n {
			break
		}
		due := (r.Status == StatusPending && !r.NextAt.After(now)) || (r.Status == StatusProcessing && r.LockedUntil.Before(now))
		if !due {
			continue
		}
		r.Status, r.LockedBy, r.LockedUntil = StatusProcessing, owner, now.Add(lease)
		r.Job.Attempts++
		out = append(out, r.Job)
	}
	return out, nil
}

// MarkDone implements queue.Store.
func (s *Store) MarkDone(_ context.Context, job queue.Job) error {
	return s.update(job, func(r *Record) { r.Status = StatusDone })
}

// MarkRetry implements queue.Store.
func (s *Store) MarkRetry(_ context.Context, job queue.Job, next time.Time, lastErr string) error {
	return s.update(job, func(r *Record) {
		r.Status, r.NextAt, r.LastError = StatusPending, next, lastErr
		r.Retries = append(r.Retries, next)
	})
}

// MarkDead implements queue.Store.
func (s *Store) MarkDead(_ context.Context, job queue.Job, lastErr string) error {
	return s.update(job, func(r *Record) { r.Status, r.LastError = StatusDead, lastErr })
}

// MarkFailed implements queue.Store.
func (s *Store) MarkFailed(_ context.Context, job queue.Job, reason string) error {
	return s.update(job, func(r *Record) { r.Status, r.LastError = StatusFailed, reason })
}

// StaleMarks returns how many Mark* calls were ignored because the claim was stale.
func (s *Store) StaleMarks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.staleMarks
}

// Get returns a copy of a job's record.
func (s *Store) Get(id uuid.UUID) Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.jobs {
		if r.Job.ID == id {
			return *r
		}
	}
	return Record{}
}

// Count returns how many jobs have the status.
func (s *Store) Count(status Status) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.jobs {
		if r.Status == status {
			n++
		}
	}
	return n
}

// Claims returns how many times Claim was called.
func (s *Store) Claims() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

// update applies f only if the job is still PROCESSING under the same claim (attempts), as a
// real store must (see queue.Store).
func (s *Store) update(job queue.Job, f func(*Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.jobs {
		if r.Job.ID == job.ID {
			if r.Status != StatusProcessing || r.Job.Attempts != job.Attempts {
				s.staleMarks++
				return nil
			}
			f(r)
			return nil
		}
	}
	return nil
}
