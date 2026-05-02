package auth

import "context"

type principalCtxKey struct{}

// WithPrincipal stores p in ctx. Called by the Auth middleware after
// successful authentication.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom retrieves the Principal from ctx. The second return is false
// when no principal has been injected (unauthenticated request).
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// MustPrincipalFrom retrieves the Principal or panics. Safe only in
// handlers that sit behind the Auth middleware.
func MustPrincipalFrom(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("auth: principal not in context — handler requires auth middleware")
	}
	return p
}
