// Package id generates identifiers. Every primary key is a UUIDv7 generated in Go (TDR-013).
package id

import "github.com/google/uuid"

// New returns a new time-ordered UUIDv7. It panics only if the operating system's random
// source fails, the same as uuid.New.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
