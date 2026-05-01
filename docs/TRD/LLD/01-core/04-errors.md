# Core: Errors (`internal/core/errors.go`)

> Sentinel errors used across modules. Plus the standard error-shape conventions.

## Purpose

Define error values that cross module boundaries. Provide guidance on wrapping, sentinel detection, and HTTP error mapping.

## Dependencies

- `errors` (stdlib)
- `fmt` (stdlib)

## Public API

```go
package core

import "errors"

// Domain-spanning sentinel errors.
//
// Most errors should be defined in the module that originates them
// (e.g., wallet.ErrInsufficientFunds). The errors here are the rare
// few that genuinely cross every module boundary.

// ErrNotFound indicates a requested resource doesn't exist or
// the caller doesn't have access (RLS-scoped: indistinguishable from absence).
var ErrNotFound = errors.New("core: not found")

// ErrInvalidArgument indicates malformed or missing input.
var ErrInvalidArgument = errors.New("core: invalid argument")

// ErrConflict indicates an operation conflicts with current state
// (e.g., trying to publish a draft rate card that's already published).
var ErrConflict = errors.New("core: conflict")

// ErrPermissionDenied indicates the actor lacks authorization.
// Distinct from ErrUnauthenticated (in auth package).
var ErrPermissionDenied = errors.New("core: permission denied")

// ErrUnavailable indicates a transient downstream failure
// (e.g., DB unreachable, external API timeout).
var ErrUnavailable = errors.New("core: unavailable")

// ErrInternal indicates an unexpected condition that's our bug.
// Should be rare; logged with stack.
var ErrInternal = errors.New("core: internal error")

// ErrSeller scope signals a seller-id mismatch (RLS-related).
// In practice, RLS returns "not found" for cross-seller, so this
// is mostly used for logging/audit when we detect such an attempt.
var ErrSellerScope = errors.New("core: seller scope violation")
```

## Error categorization

Use `errors.Is(err, sentinel)` to categorize. Domain errors should be unwrappable to the right sentinel:

```go
// In wallet/errors.go:
var ErrInsufficientFunds = fmt.Errorf("wallet: insufficient funds: %w", core.ErrInvalidArgument)
```

Then callers can:

```go
if errors.Is(err, core.ErrInvalidArgument) {
    return http.StatusBadRequest
}
```

This lets HTTP layer route errors without knowing every domain-specific error.

## Wrapping discipline

### When to wrap

Wrap with context when crossing layers:

```go
// repo.go (DB layer)
func (r *repo) GetWallet(ctx context.Context, sellerID core.SellerID) (*WalletAccount, error) {
    row := r.db.QueryRow(ctx, getWalletSQL, sellerID)
    var w WalletAccount
    err := row.Scan(&w.ID, &w.Balance, ...)
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, core.ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("wallet.repo.GetWallet: %w", err)
    }
    return &w, nil
}

// service.go (domain layer)
func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, ...) (HoldID, error) {
    wallet, err := s.repo.GetWallet(ctx, sellerID)
    if err != nil {
        return HoldID{}, fmt.Errorf("wallet.Reserve: %w", err)
    }
    ...
}
```

The wrapping chain becomes:
```
"wallet.Reserve: wallet.repo.GetWallet: connection failed"
```
Each layer adds context; the originating sentinel is preserved.

### When NOT to wrap

- Returning a sentinel directly: `return nil, ErrInvalidArgument` — already self-explanatory.
- Inside the same module's internal calls if context isn't useful: just pass through.

### Format string

Use `<package>.<function>:` prefix:

```go
fmt.Errorf("wallet.Reserve: not enough funds for seller %s: %w", sellerID, ErrInsufficientFunds)
```

NOT:
```go
fmt.Errorf("error: %v", err)              // no prefix
fmt.Errorf("Reserve failed: %v", err)     // %v doesn't preserve wrapping
fmt.Errorf("wallet.Reserve: %s", err)     // %s doesn't preserve wrapping
```

## HTTP error mapping

Centralized in `api/http/errors.go`:

```go
package http

import (
    "errors"
    "net/http"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/wallet"
    // ... other modules with public sentinel errors
)

// ErrorResponse is the standard JSON error shape returned by all endpoints.
type ErrorResponse struct {
    Error ErrorBody `json:"error"`
}

type ErrorBody struct {
    Type      string `json:"type"`
    Code      string `json:"code"`
    Message   string `json:"message"`
    Param     string `json:"param,omitempty"`
    RequestID string `json:"request_id"`
    DocURL    string `json:"doc_url,omitempty"`
}

// mapError returns (status, code) for a domain error.
//
// Order matters: more-specific sentinels first. Default catch-all maps to 500.
func mapError(err error) (int, ErrorBody) {
    switch {
    case errors.Is(err, auth.ErrUnauthenticated):
        return 401, ErrorBody{Type: "authentication_error", Code: "unauthenticated"}
    case errors.Is(err, core.ErrPermissionDenied):
        return 403, ErrorBody{Type: "permission_error", Code: "permission_denied"}
    case errors.Is(err, core.ErrNotFound):
        return 404, ErrorBody{Type: "not_found", Code: "not_found"}
    case errors.Is(err, core.ErrConflict):
        return 409, ErrorBody{Type: "conflict", Code: "conflict"}
    case errors.Is(err, core.ErrInvalidArgument):
        return 400, ErrorBody{Type: "invalid_request", Code: "invalid_argument"}
    case errors.Is(err, wallet.ErrInsufficientFunds):
        return 402, ErrorBody{Type: "payment_required", Code: "insufficient_funds"}
    case errors.Is(err, core.ErrUnavailable):
        return 503, ErrorBody{Type: "unavailable", Code: "unavailable"}
    default:
        return 500, ErrorBody{Type: "internal_error", Code: "internal"}
    }
}

// writeError writes a standardized error response.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
    status, body := mapError(err)
    body.RequestID = requestIDFrom(r.Context())

    if status >= 500 {
        // Server error: log full chain
        log := loggingFrom(r.Context())
        log.ErrorContext(r.Context(), "request failed", slog.Any("error", err))
        body.Message = "Internal server error. Reference: " + body.RequestID
    } else {
        body.Message = err.Error()  // safe to expose
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(ErrorResponse{Error: body})
}
```

## Sentinel error patterns per module

Each module declares its own:

```go
// internal/wallet/errors.go
package wallet

import (
    "errors"
    "fmt"

    "github.com/pikshipp/pikshipp/internal/core"
)

var (
    // ErrInsufficientFunds is returned when a wallet operation would exceed the
    // available balance + credit limit + grace cap.
    ErrInsufficientFunds = fmt.Errorf("wallet: insufficient funds: %w", core.ErrInvalidArgument)

    // ErrHoldNotFound is returned when a Confirm/Release references a hold ID
    // that doesn't exist or is already resolved.
    ErrHoldNotFound = fmt.Errorf("wallet: hold not found: %w", core.ErrNotFound)

    // ErrHoldExpired is returned when a Confirm references a hold past its TTL.
    ErrHoldExpired = errors.New("wallet: hold expired")

    // ErrGraceCapBreached is returned when a Post would exceed the configured
    // grace cap. Triggers seller suspension.
    ErrGraceCapBreached = errors.New("wallet: grace cap breached")

    // ErrInvariantViolation is returned by the daily check job. P0 alert.
    ErrInvariantViolation = errors.New("wallet: invariant violation")
)
```

Each error wraps a `core.Err*` sentinel where the categorization is meaningful. `ErrHoldExpired` and `ErrGraceCapBreached` don't wrap because their HTTP semantics (409, 402) are explicitly handled.

## Error logging discipline

```go
// At the boundary where you decide to handle vs. propagate:

if errors.Is(err, wallet.ErrInsufficientFunds) {
    // Expected business condition; log Info, return to user
    log.InfoContext(ctx, "booking blocked: insufficient funds",
        slog.String("seller_id", sellerID.String()))
    return nil, err
}

if errors.Is(err, core.ErrUnavailable) {
    // Transient; log Warn, return to user with retry guidance
    log.WarnContext(ctx, "downstream unavailable", slog.Any("error", err))
    return nil, err
}

// Anything else: log Error with full chain, return generic
log.ErrorContext(ctx, "unexpected error", slog.Any("error", err))
return nil, fmt.Errorf("wallet.Reserve: %w", err)
```

## What never appears in errors

- Raw stack traces in `Error()` strings (use logging for stack).
- PII (don't put email or phone in error messages user-visible to other parties).
- DB-specific error text (translate via `errors.Is`).
- Vendor-specific text (`"Stripe error 4xx..."` — translate at the adapter).

## Testing

```go
func TestWallet_ErrorWrapping(t *testing.T) {
    err := wallet.ErrInsufficientFunds
    if !errors.Is(err, core.ErrInvalidArgument) {
        t.Errorf("ErrInsufficientFunds should wrap core.ErrInvalidArgument")
    }
}

func TestHTTP_ErrorMapping(t *testing.T) {
    cases := []struct {
        err  error
        want int
    }{
        {wallet.ErrInsufficientFunds, 402},
        {core.ErrNotFound, 404},
        {auth.ErrUnauthenticated, 401},
        {errors.New("random"), 500},
    }
    for _, c := range cases {
        status, _ := mapError(c.err)
        if status != c.want {
            t.Errorf("mapError(%v) = %d; want %d", c.err, status, c.want)
        }
    }
}
```

## Performance

Error allocation is cheap; the common case (no error returned) costs nothing. Sentinels are package-level vars with no per-call cost.

For very-hot paths where allocation matters (rare in our stack), pre-define wrapped errors instead of building per-call:

```go
// Pre-built (good)
var ErrSpecific = fmt.Errorf("...: %w", parent)

// Per-call (avoid in hot loops)
return fmt.Errorf("...: %w", parent)  // allocates on every call
```

## Open questions

- Should we add structured error fields (e.g., `Param string`, `Hint string`)? At v0 we keep it simple. If we need parametrized errors for the dashboard, add a typed error struct that implements `error`.
- Stack traces: stdlib doesn't capture; do we add a wrapper? No — we get stack from `slog.AddSource: true` at the log site. Domain errors don't carry stacks.
