// Package policy is the configurability substrate. Every domain service that
// needs "what's the rule for THIS seller?" goes through policy.Engine.Resolve().
//
// Per LLD §03-services/01-policy-engine.
package policy

import "errors"

var (
	ErrUnknownKey   = errors.New("policy: unknown key")
	ErrLocked       = errors.New("policy: setting is locked; cannot override")
	ErrInvalidValue = errors.New("policy: value does not match key's type")
)
