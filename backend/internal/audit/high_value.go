package audit

import "strings"

// HighValueActions is the set of action names (or prefixes) that REQUIRE
// synchronous in-tx emit. Per LLD §03-services/02-audit §"High-value action list".
//
// A trailing "." marks a prefix match; an entry without "." is matched
// exactly. Keep this list narrow — every entry costs one in-tx round trip.
var HighValueActions = []string{
	// Financial — every wallet movement.
	"wallet.",
	"cod.remitted",
	"weight_dispute.",

	// Identity / lifecycle.
	"seller.kyc_",
	"seller.suspended",
	"seller.reactivated",
	"seller.wound_down",
	"user.role_granted",
	"user.role_revoked",
	"user.locked",

	// Privileged ops.
	"ops.manual_adjustment",
	"ops.kyc_override",
	"ops.cross_seller_view",

	// Policy & contract — drives runtime behavior.
	"policy.lock_set",
	"policy.lock_removed",
	"policy.seller_type_default_changed",
	"contract.signed",
	"contract.terminated",
	"contract.amended",

	// Carrier credentials.
	"carrier.credential_rotated",
}

// IsHighValue reports whether action requires sync emit.
// Entries ending with "." or "_" are prefix matches; others are exact.
func IsHighValue(action string) bool {
	for _, entry := range HighValueActions {
		last := entry[len(entry)-1]
		if last == '.' || last == '_' {
			if strings.HasPrefix(action, entry) {
				return true
			}
			continue
		}
		if action == entry {
			return true
		}
	}
	return false
}
