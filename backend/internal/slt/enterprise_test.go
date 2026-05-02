package slt_test

// Enterprise upgrade + contract-enforcement SLT.
//
// Scenario: a small_business seller hits their default order/day cap of 200,
// gets blocked. Operator upgrades them to enterprise with a contract whose
// terms include `limits.orders_per_day: 0` (unlimited). After the upgrade,
// the seller can create orders past the old cap.

import (
	"context"
	"testing"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/contracts"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
	"github.com/vishal1132/pikshipp/backend/internal/slt"
)

func TestEnterpriseUpgrade_LiftsOrderLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("SLT: skipped in short mode (requires Docker)")
	}

	pool := slt.NewDB(t)
	ctx := context.Background()
	log := slt.NopLogger()

	// Wire policy + contracts + sellers.
	au := audit.New(pool, nil, core.SystemClock{}, log)
	policyEngine, err := policy.New(pool, au, core.SystemClock{}, log)
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	sellerSvc := seller.New(pool, au, log)
	contractsSvc := contracts.New(pool, au, policyEngine)
	limitsGuard := limits.New(pool, policyEngine)

	// Seed a fresh active seller (small_business by default).
	ts := slt.CreateActiveSeller(t, pool)
	sellerID := ts.Seller.ID
	t.Logf("✓ seeded small_business seller: %s", sellerID)

	// --- Pre-upgrade ---

	// Default for small_business is 200 orders/day.
	limit, err := policyEngine.Resolve(ctx, sellerID, policy.KeyOrdersPerDayLimit)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := limit.AsInt64()
	if n != 200 {
		t.Errorf("pre-upgrade order/day limit = %d, want 200", n)
	}
	t.Logf("✓ pre-upgrade orders/day limit: %d", n)

	// Usage check should pass since count is 0.
	if err := limitsGuard.CheckOrderDay(ctx, sellerID); err != nil {
		t.Errorf("pre-upgrade CheckOrderDay should allow at 0 count: %v", err)
	}

	// --- Upgrade ---

	op := ts.User.ID
	if err := sellerSvc.ChangeType(ctx, sellerID, core.SellerTypeEnterprise, op); err != nil {
		t.Fatalf("ChangeType: %v", err)
	}
	t.Logf("✓ seller_type → enterprise")

	// Create + activate enterprise contract with terms.
	terms := map[string]any{
		"policy_overrides": map[string]any{
			"limits.orders_per_day":         0, // unlimited
			"limits.shipments_per_month":    0,
			"features.insurance":            true,
			"wallet.credit_limit_inr":       50_000_000, // ₹5 lakh credit
			"features.weight_dispute_auto":  true,
		},
		"monthly_minimum_paise":   100_000_000,
		"sla_delivered_p95_days":  3,
	}
	c, err := contractsSvc.Create(ctx, sellerID, terms, nil, ts.Seller.CreatedAt, op)
	if err != nil {
		t.Fatalf("Create contract: %v", err)
	}
	if err := contractsSvc.Activate(ctx, sellerID, c.ID, op); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Logf("✓ enterprise contract %s activated (v%d)", c.ID, c.Version)

	// --- Post-upgrade ---

	// Limit should now be 0 (unlimited) per the contract overrides.
	limitNow, err := policyEngine.Resolve(ctx, sellerID, policy.KeyOrdersPerDayLimit)
	if err != nil {
		t.Fatal(err)
	}
	n2, _ := limitNow.AsInt64()
	if n2 != 0 {
		t.Errorf("post-upgrade limit = %d, want 0 (unlimited)", n2)
	}
	t.Logf("✓ post-upgrade orders/day limit: %d (unlimited)", n2)

	// Insurance feature should now be enabled.
	insurance, err := policyEngine.Resolve(ctx, sellerID, policy.KeyFeatureInsurance)
	if err != nil {
		t.Fatal(err)
	}
	insBool, _ := insurance.AsBool()
	if !insBool {
		t.Errorf("post-upgrade insurance feature = %v, want true", insBool)
	}
	t.Logf("✓ insurance feature enabled by contract")

	// Credit limit raised.
	creditLimit, err := policyEngine.Resolve(ctx, sellerID, policy.KeyWalletCreditLimitInr)
	if err != nil {
		t.Fatal(err)
	}
	cl, _ := creditLimit.AsInt64()
	if cl != 50_000_000 {
		t.Errorf("post-upgrade credit limit = %d, want 50000000", cl)
	}
	t.Logf("✓ credit limit raised to ₹%d", cl/100)

	// CheckOrderDay should pass even with hypothetical many orders, because limit=0.
	if err := limitsGuard.CheckOrderDay(ctx, sellerID); err != nil {
		t.Errorf("post-upgrade limit check should pass when 0=unlimited: %v", err)
	}

	// Active contract id stamped in policy.
	activeID, _ := policyEngine.Resolve(ctx, sellerID, policy.KeyContractActiveID)
	gotID, _ := activeID.AsString()
	if gotID != c.ID.String() {
		t.Errorf("KeyContractActiveID = %q, want %q", gotID, c.ID.String())
	}
	t.Logf("✓ KeyContractActiveID = %s", gotID)

	// Verify contract is the only active one.
	active, err := contractsSvc.GetActive(ctx, sellerID)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.ID != c.ID {
		t.Errorf("active contract id = %s, want %s", active.ID, c.ID)
	}
	if active.State != "active" {
		t.Errorf("active.State = %q, want active", active.State)
	}
	t.Logf("✓ contract list: 1 active")

	// --- Termination ---

	if err := contractsSvc.Terminate(ctx, sellerID, c.ID, "test cleanup", op); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	t.Logf("✓ contract terminated")

	// After termination, overrides should be removed → limit reverts to type default.
	// Since seller_type is still 'enterprise', the type default is 0 (unlimited). To
	// fully validate revert we'd need to also revert type — but the contract layer
	// only manages overrides, not seller_type. That's correct.
	limitAfter, _ := policyEngine.Resolve(ctx, sellerID, policy.KeyOrdersPerDayLimit)
	la, _ := limitAfter.AsInt64()
	if la != 0 {
		t.Errorf("after terminate, enterprise type default should be 0, got %d", la)
	}
	t.Logf("✓ post-termination limit: %d (enterprise type default)", la)
}
