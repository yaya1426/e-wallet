// Package banktest provides an in-memory bank.Gateway for unit tests: it records every call
// and can be scripted per idempotency key (e.g. "time out twice, then accept").
package banktest

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/bank"
	"github.com/yaya1426/e-wallet/internal/platform/money"
)

// Call is one recorded call. Exactly one of Debit and Payout is set for a create call;
// neither is set for GetOperation.
type Call struct {
	Op     string // "CreateDebit", "CreatePayout" or "GetOperation"
	Key    uuid.UUID
	Debit  *bank.DebitRequest
	Payout *bank.PayoutRequest
}

// Response is one scripted answer. A non-nil Err is returned as is; otherwise a create call
// returns Acceptance and GetOperation returns Operation.
type Response struct {
	Acceptance bank.Acceptance
	Operation  bank.Operation
	Err        error
}

// Fake is a bank.Gateway. Without a script it behaves like a healthy bank: a create call
// accepts (the same key again returns the same acceptance, with no new operation) and
// GetOperation returns the stored operation or bank.ErrNotFound. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	calls   []Call
	scripts map[uuid.UUID][]Response
	ops     map[uuid.UUID]bank.Operation
	nextRef int
}

var _ bank.Gateway = (*Fake)(nil)

// New returns an empty fake.
func New() *Fake {
	return &Fake{scripts: map[uuid.UUID][]Response{}, ops: map[uuid.UUID]bank.Operation{}}
}

// Script queues answers for the next calls with key, used one per call in order; after they
// are used up the default behaviour applies again.
func (f *Fake) Script(key uuid.UUID, responses ...Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[key] = append(f.scripts[key], responses...)
}

// SetOperation stores (or replaces) the operation the bank knows for op.IdempotencyKey.
func (f *Fake) SetOperation(op bank.Operation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops[op.IdempotencyKey] = op
}

// Calls returns a copy of the recorded calls, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CreateDebit records the call and answers it.
func (f *Fake) CreateDebit(_ context.Context, req bank.DebitRequest) (bank.Acceptance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Op: "CreateDebit", Key: req.IdempotencyKey, Debit: &req})
	return f.create(req.IdempotencyKey, bank.KindDebit, req.Amount)
}

// CreatePayout records the call and answers it.
func (f *Fake) CreatePayout(_ context.Context, req bank.PayoutRequest) (bank.Acceptance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Op: "CreatePayout", Key: req.IdempotencyKey, Payout: &req})
	return f.create(req.IdempotencyKey, bank.KindPayout, req.Amount)
}

// GetOperation records the call and answers it.
func (f *Fake) GetOperation(_ context.Context, key uuid.UUID) (bank.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Op: "GetOperation", Key: key})
	if r, ok := f.pop(key); ok {
		return r.Operation, r.Err
	}
	op, ok := f.ops[key]
	if !ok {
		return bank.Operation{}, bank.ErrNotFound
	}
	return op, nil
}

func (f *Fake) create(key uuid.UUID, kind bank.Kind, amount money.Money) (bank.Acceptance, error) {
	if r, ok := f.pop(key); ok {
		if r.Err == nil {
			f.remember(key, kind, amount, r.Acceptance.BankReference)
		}
		return r.Acceptance, r.Err
	}
	if op, ok := f.ops[key]; ok { // idempotent repeat
		return bank.Acceptance{BankReference: op.BankReference}, nil
	}
	f.nextRef++
	ref := fmt.Sprintf("FAKE-%06d", f.nextRef)
	f.remember(key, kind, amount, ref)
	return bank.Acceptance{BankReference: ref}, nil
}

// remember stores a PENDING operation for an accepted create, unless one already exists.
func (f *Fake) remember(key uuid.UUID, kind bank.Kind, amount money.Money, ref string) {
	if _, ok := f.ops[key]; ok {
		return
	}
	f.ops[key] = bank.Operation{IdempotencyKey: key, BankReference: ref, Kind: kind, Status: bank.StatusPending, Amount: amount}
}

func (f *Fake) pop(key uuid.UUID) (Response, bool) {
	queue := f.scripts[key]
	if len(queue) == 0 {
		return Response{}, false
	}
	f.scripts[key] = queue[1:]
	return queue[0], true
}
