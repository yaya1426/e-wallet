package banksim

import (
	"sync"

	"github.com/google/uuid"
)

// operation is one accepted debit or payout.
type operation struct {
	Key            uuid.UUID
	BankReference  string
	Kind           string // DEBIT | PAYOUT
	Status         string // PENDING | SUCCEEDED | FAILED
	AmountMinor    int64
	Currency       string
	FailureCode    string
	FailureMessage string
}

// answer is the stored response to a create request, replayed for the same key and body.
type answer struct {
	fingerprint request // what was asked, to detect a reused key
	status      int
	body        []byte
}

// store keeps the simulator's state in memory (TDR-008 §2: a test double, lost on restart).
type store struct {
	mu        sync.Mutex
	answers   map[uuid.UUID]answer     // every create answer (202 and 422), by idempotency key
	ops       map[uuid.UUID]*operation // accepted operations, by idempotency key
	overrides map[string]string        // one-shot scenario per IBAN (/admin/scenarios)
}

func newStore() *store {
	return &store{answers: map[uuid.UUID]answer{}, ops: map[uuid.UUID]*operation{}, overrides: map[string]string{}}
}

// operation returns a copy of the operation with key.
func (s *store) operation(key uuid.UUID) (operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[key]
	if !ok {
		return operation{}, false
	}
	return *op, true
}

// finish sets the final status of an operation and returns a copy.
func (s *store) finish(key uuid.UUID, status, failureCode, failureMessage string) operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.ops[key]
	op.Status, op.FailureCode, op.FailureMessage = status, failureCode, failureMessage
	return *op
}

func (s *store) setOverride(iban, scenario string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[iban] = scenario
}
