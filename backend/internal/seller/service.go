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
	ID              core.SellerID    `json:"id"`
	LegalName       string           `json:"legal_name"`
	DisplayName     string           `json:"display_name"`
	SellerType      core.SellerType  `json:"seller_type"`
	LifecycleState  LifecycleState   `json:"lifecycle_state"`
	GSTIN           string           `json:"gstin,omitempty"`
	PAN             string           `json:"pan,omitempty"`
	BillingEmail    string           `json:"billing_email"`
	SupportEmail    string           `json:"support_email"`
	PrimaryPhone    string           `json:"primary_phone"`
	SignupSource    string           `json:"signup_source"`
	FoundingUserID  core.UserID      `json:"founding_user_id"`
	SuspendedReason string           `json:"suspended_reason,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// KYCApplication is the seller's KYC data.
type KYCApplication struct {
	SellerID        core.SellerID `json:"seller_id"`
	State           string        `json:"state"`
	LegalName       string        `json:"legal_name"`
	GSTIN           string        `json:"gstin"`
	PAN             string        `json:"pan"`
	BusinessAddress core.Address  `json:"business_address"`
	SubmittedAt     *time.Time    `json:"submitted_at,omitempty"`
	DecidedAt       *time.Time    `json:"decided_at,omitempty"`
	DecisionReason  string        `json:"decision_reason,omitempty"`
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
