package httpserver

import (
	"context"

	"github.com/google/uuid"
)

type userIDKey struct{}

// WithUserID returns a copy of ctx carrying the authenticated user's ID. The auth middleware
// sets it; the key lives here so every module can read it without importing identity.
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

// UserID returns the authenticated user's ID, if the request passed the auth middleware.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	userID, ok := ctx.Value(userIDKey{}).(uuid.UUID)
	return userID, ok
}
