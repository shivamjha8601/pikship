// Package seller manages seller organisations, lifecycle transitions, and KYC.
//
// Per LLD §03-services/09-seller.
package seller

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Service is the public API of the seller module.
type Service interface {
	// Provision creates a new seller in the 'provisioning' lifecycle state.
	Provision(ctx context.Context, in ProvisionInput) (Seller, error)

	// Get returns a seller by ID. Uses admin pool — bypasses RLS.
	Get(ctx context.Context, id core.SellerID) (Seller, error)

	// Activate transitions a seller from sandbox → active.
	Activate(ctx context.Context, id core.SellerID, reason string) error

	// Suspend transitions a seller to suspended.
	Suspend(ctx context.Context, id core.SellerID, reason, category string, until *time.Time) error

	// Reinstate transitions a suspended seller back to active.
	Reinstate(ctx context.Context, id core.SellerID, reason string) error

	// WoundDown transitions a seller to wound_down.
	WindDown(ctx context.Context, id core.SellerID, reason string) error

	// SubmitKYC updates the KYC application to 'submitted'.
	SubmitKYC(ctx context.Context, id core.SellerID, app KYCApplication) error

	// ApproveKYC transitions KYC to 'approved' and updates seller fields.
	ApproveKYC(ctx context.Context, id core.SellerID, reason string, by core.UserID) error

	// RejectKYC transitions KYC to 'rejected'.
	RejectKYC(ctx context.Context, id core.SellerID, reason string, by core.UserID) error

	// GetKYC returns the KYC application for the seller.
	GetKYC(ctx context.Context, id core.SellerID) (KYCApplication, error)
}

// LifecycleState is the seller's lifecycle state.
type LifecycleState string

const (
	StateProvisioning LifecycleState = "provisioning"
	StateSandbox      LifecycleState = "sandbox"
	StateActive       LifecycleState = "active"
	StateSuspended    LifecycleState = "suspended"
	StateWoundDown    LifecycleState = "wound_down"
)

// Seller is the public view of the seller table.
type Seller struct {
	ID              core.SellerID
	LegalName       string
	DisplayName     string
	SellerType      core.SellerType
	LifecycleState  LifecycleState
	GSTIN           string
	PAN             string
	BillingEmail    string
	SupportEmail    string
	PrimaryPhone    string
	SignupSource     string
	FoundingUserID  core.UserID
	SuspendedReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// KYCApplication is the seller's KYC data.
type KYCApplication struct {
	SellerID       core.SellerID
	State          string
	LegalName      string
	GSTIN          string
	PAN            string
	BusinessAddress core.Address
	SubmittedAt    *time.Time
	DecidedAt      *time.Time
	DecisionReason string
}

// ProvisionInput carries the data needed to create a new seller.
type ProvisionInput struct {
	LegalName      string
	DisplayName    string
	SellerType     core.SellerType
	BillingEmail   string
	SupportEmail   string
	PrimaryPhone   string
	SignupSource    string
	FoundingUserID core.UserID
}
