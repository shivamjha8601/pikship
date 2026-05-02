package audit

import "context"

// ActorFromContext extracts an Actor from ctx. If the auth middleware has
// populated ctx via WithActor (called from internal/auth on every
// authenticated request), that Actor is returned. Otherwise: ActorSystem.
//
// Domain code that already knows the actor (worker jobs, webhooks) should
// build an Actor explicitly rather than relying on this fallback.
func ActorFromContext(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorCtxKey{}).(Actor); ok {
		return a
	}
	return Actor{Kind: ActorSystem, Ref: "no-principal"}
}

// WithActor returns a child context carrying the given Actor. Called from
// the auth middleware after Authenticate succeeds — domain code then
// reads via ActorFromContext.
//
// auth depends on audit (one-way) for this; audit does NOT import auth.
// This avoids the package cycle without forcing an "actor" subpackage.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, a)
}

// actorCtxKey is the unexported key under which Actor is stored. The
// empty struct guarantees the key is only producible inside this package.
type actorCtxKey struct{}
