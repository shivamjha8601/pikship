package core

import "errors"

// Domain-spanning sentinel errors. Per LLD §01-core/04-errors:
// most error values live in the module that originates them; this file
// only declares the few that genuinely cross every module boundary.

var (
	// ErrNotFound — requested resource doesn't exist or RLS hides it.
	ErrNotFound = errors.New("core: not found")

	// ErrInvalidArgument — malformed or missing input.
	ErrInvalidArgument = errors.New("core: invalid argument")

	// ErrConflict — operation conflicts with current state.
	ErrConflict = errors.New("core: conflict")

	// ErrPermissionDenied — actor lacks authorization. Distinct from
	// auth.ErrUnauthenticated.
	ErrPermissionDenied = errors.New("core: permission denied")

	// ErrUnavailable — transient downstream failure.
	ErrUnavailable = errors.New("core: unavailable")

	// ErrInternal — unexpected condition; our bug. Rare, logged with stack.
	ErrInternal = errors.New("core: internal error")

	// ErrSellerScope — seller-id missing or mismatched on a scoped operation.
	ErrSellerScope = errors.New("core: seller scope violation")
)
