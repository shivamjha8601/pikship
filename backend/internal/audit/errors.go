package audit

import "errors"

// ErrNotHighValue means the caller used Emit (in-tx, sync) for an action
// that is not on the HighValueActions allowlist. Use EmitAsync instead.
//
// Why a hard error and not a silent passthrough: synchronous emit costs an
// extra SELECT + INSERT inside the caller's tx. We only want to pay that
// cost for events whose loss is intolerable. The compile-time-friendly
// check (the prefix list in high_value.go) plus this runtime guard catch
// drift in either direction.
var ErrNotHighValue = errors.New("audit: action is not in HighValueActions; use EmitAsync")

// ErrChainBroken indicates VerifyChain detected a hash mismatch. Triggers
// the AuditChainBrokenForSeller alert (P0).
var ErrChainBroken = errors.New("audit: hash chain mismatch")
