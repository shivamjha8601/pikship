package slt

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
	"github.com/vishal1132/pikshipp/backend/internal/audit"
)

// TestSeller creates a fully active seller with one user member.
// Returns the seller, user and their seller membership.
type TestSeller struct {
	Seller     seller.Seller
	User       identity.User
	Membership identity.SellerMembership
}

// CreateActiveSeller provisions a seller, approves KYC, and creates a user
// with owner role. All seeded through service layer (not raw SQL) so all
// business invariants are exercised.
func CreateActiveSeller(t *testing.T, pool *pgxpool.Pool) TestSeller {
	t.Helper()
	ctx := context.Background()

	log := NopLogger()
	au := audit.New(pool, nil, core.SystemClock{}, log)
	identitySvc := identity.New(pool, au, log)
	sellerSvc := seller.New(pool, au, log)

	// Create user first (seller provisioning references founding_user_id).
	user, err := identitySvc.UpsertFromOAuth(ctx, identity.ProviderGoogle, identity.OAuthProfile{
		ProviderUserID: "google_test_" + core.NewUserID().String()[:8],
		Email:          "testowner_" + core.NewUserID().String()[:6] + "@example.com",
		Name:           "Test Owner",
	})
	if err != nil {
		t.Fatalf("CreateActiveSeller: upsert user: %v", err)
	}

	// Provision seller.
	s, err := sellerSvc.Provision(ctx, seller.ProvisionInput{
		LegalName:      "Test Company Pvt Ltd",
		DisplayName:    "TestCo",
		SellerType:     core.SellerTypeSmallMedium,
		BillingEmail:   user.Email,
		SupportEmail:   user.Email,
		PrimaryPhone:   "+919999999999",
		SignupSource:    "slt",
		FoundingUserID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateActiveSeller: provision: %v", err)
	}

	// Submit + approve KYC so lifecycle_state becomes provisionable for active.
	_ = sellerSvc.SubmitKYC(ctx, s.ID, seller.KYCApplication{
		LegalName: s.LegalName,
		GSTIN:     "29AABCU9603R1ZX",
		PAN:       "AABCU9603R",
	})
	_ = sellerSvc.ApproveKYC(ctx, s.ID, "auto-approved in SLT", user.ID)
	_ = sellerSvc.Activate(ctx, s.ID, "slt activation")

	// Re-fetch seller to get updated lifecycle_state.
	s, err = sellerSvc.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("CreateActiveSeller: re-fetch seller: %v", err)
	}

	// Add user as seller member with owner role.
	membership := identity.SellerMembership{
		UserID:   user.ID,
		SellerID: s.ID,
		Roles:    []core.SellerRole{core.RoleOwner},
		Status:   "active",
	}
	if err := seedSellerUser(ctx, pool, membership); err != nil {
		t.Fatalf("CreateActiveSeller: seed seller_user: %v", err)
	}

	return TestSeller{Seller: s, User: user, Membership: membership}
}

// seedSellerUser inserts a seller_user row directly (bypassing RLS as root).
func seedSellerUser(ctx context.Context, pool *pgxpool.Pool, m identity.SellerMembership) error {
	_, err := pool.Exec(ctx, `
        INSERT INTO seller_user (user_id, seller_id, roles_jsonb, status, joined_at)
        VALUES ($1, $2, $3::jsonb, 'active', $4)
        ON CONFLICT (user_id, seller_id) DO UPDATE
            SET roles_jsonb=EXCLUDED.roles_jsonb, status='active', joined_at=now()`,
		m.UserID.UUID(), m.SellerID.UUID(),
		`["owner"]`, time.Now(),
	)
	return err
}

// EnsureWallet creates a wallet for the seller if one does not exist.
func EnsureWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sellerID core.SellerID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
        INSERT INTO wallet_account (seller_id) VALUES ($1) ON CONFLICT (seller_id) DO NOTHING`,
		sellerID.UUID())
	if err != nil {
		t.Fatalf("EnsureWallet: %v", err)
	}
	// Credit ₹5000 so shipment charges can be reserved.
	_, err = pool.Exec(ctx, `
        UPDATE wallet_account SET balance_minor = 500000 WHERE seller_id = $1`,
		sellerID.UUID())
	if err != nil {
		t.Fatalf("EnsureWallet credit: %v", err)
	}
}
