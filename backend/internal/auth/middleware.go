package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Middleware returns a Chi-compatible middleware that authenticates each
// request, injects the Principal into context, and populates the audit actor.
// Returns 401 on unauthenticated; 500 on unexpected errors.
func Middleware(a Authenticator, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := a.Authenticate(r.Context(), r)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) ||
					errors.Is(err, ErrSessionExpired) ||
					errors.Is(err, ErrSessionRevoked) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				log.ErrorContext(r.Context(), "auth middleware: unexpected error", slog.String("err", err.Error()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			ctx := WithPrincipal(r.Context(), principal)

			// Populate audit actor so any audit.Emit in this request picks
			// up the right principal without passing it explicitly.
			ctx = audit.WithActor(ctx, audit.Actor{
				Kind: audit.ActorKind(principal.UserKind),
				Ref:  principal.UserID.String(),
			})

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole returns a Chi-compatible middleware that checks the Principal
// has at least one of the given roles. Must be used after Middleware.
func RequireRole(roles ...core.SellerRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok || !core.HasAnyRole(p.Roles, roles) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
