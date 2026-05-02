package auth

import (
	"context"
	"net/http"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Authenticator is the public API for session authentication.
type Authenticator interface {
	// Authenticate reads the session token from the request, validates it,
	// and returns the Principal. Returns ErrUnauthenticated when no valid
	// session is present.
	Authenticate(ctx context.Context, r *http.Request) (Principal, error)

	// IssueCredentials creates a new session for the given user/seller pair
	// and returns the opaque token (for cookie or bearer header).
	IssueCredentials(ctx context.Context, userID core.UserID, sellerID *core.SellerID) (Credentials, error)

	// Revoke invalidates the session identified by the raw token.
	Revoke(ctx context.Context, token string) error

	// RevokeAllForUser invalidates every active session for the user.
	RevokeAllForUser(ctx context.Context, userID core.UserID) error
}
