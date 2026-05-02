// Package core provides fundamental types used across all Pikshipp modules.
//
// Anything imported from internal/core MUST be:
//   - Pure (no I/O, no global state).
//   - Stable (rarely changes; downstream modules pin to its shapes).
//
// Domain modules import core freely. Core never imports a domain module.
package core
