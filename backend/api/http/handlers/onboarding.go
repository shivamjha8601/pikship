package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
)

// OnboardingDeps wires the services needed by the onboarding flow.
type OnboardingDeps struct {
	Identity identity.Service
	Seller   seller.Service
	Auth     auth.Authenticator
	DevMode  bool // when true, exposes /v1/auth/dev-login (NEVER enable in prod)

	// Google OAuth — when nil, /v1/auth/google/* return 503.
	Google             GoogleOAuthAdapter
	GoogleFrontendURL  string // browser is 302'd here on successful callback
}

// DevLoginHandler simulates a Google OAuth callback for local/test use.
// Body: {"email": "...", "name": "..."}.
// Returns: {"token": "...", "expires_at": "...", "user": {...}, "sellers": [...]}.
//
// Behavior matches the real OAuth callback exactly:
// - identity.UpsertFromOAuth — creates user if new, updates if existing
// - auth.IssueCredentials   — opaque session token, RLS-unscoped
// - returns memberships     — frontend uses this to drive the wizard
func DevLoginHandler(d OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.DevMode {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint disabled"})
			return
		}
		var req struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := decode(r, &req); err != nil || req.Email == "" {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}

		user, err := d.Identity.UpsertFromOAuth(r.Context(), identity.ProviderGoogle, identity.OAuthProfile{
			ProviderUserID: "dev_" + req.Email,
			Email:          req.Email,
			Name:           req.Name,
			Raw:            map[string]any{"dev_login": true},
		})
		if err != nil {
			writeError(w, r, err)
			return
		}

		creds, err := d.Auth.IssueCredentials(r.Context(), user.ID, nil)
		if err != nil {
			writeError(w, r, err)
			return
		}

		sellers, _ := d.Identity.ListUserSellers(r.Context(), user.ID)
		writeJSON(w, http.StatusOK, map[string]any{
			"token":      creds.Token,
			"expires_at": creds.ExpiresAt,
			"user":       user,
			"sellers":    sellers,
		})
	}
}

// ProvisionSellerHandler creates a new seller for the authenticated user.
// First call by a brand-new user; subsequent calls add additional sellers.
// Body matches seller.ProvisionInput minus FoundingUserID (taken from session).
//
// Side effects:
// - Creates seller in 'provisioning' state
// - Creates seller_user link with owner role
// - Creates wallet_account
// - Returns a NEW session token scoped to the new seller (so frontend can
//   continue without a separate select-seller call)
func ProvisionSellerHandler(d OnboardingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())

		var body struct {
			LegalName    string `json:"legal_name"`
			DisplayName  string `json:"display_name"`
			BillingEmail string `json:"billing_email"`
			SupportEmail string `json:"support_email"`
			PrimaryPhone string `json:"primary_phone"`
			SignupSource string `json:"signup_source"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, r, err)
			return
		}
		if body.LegalName == "" || body.DisplayName == "" || body.PrimaryPhone == "" {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		if body.BillingEmail == "" {
			body.BillingEmail = "billing+" + p.UserID.String()[:8] + "@example.com"
		}
		if body.SupportEmail == "" {
			body.SupportEmail = body.BillingEmail
		}
		if body.SignupSource == "" {
			body.SignupSource = "self_signup"
		}

		s, err := d.Seller.Provision(r.Context(), seller.ProvisionInput{
			LegalName:      body.LegalName,
			DisplayName:    body.DisplayName,
			SellerType:     core.SellerTypeSmallMedium,
			BillingEmail:   body.BillingEmail,
			SupportEmail:   body.SupportEmail,
			PrimaryPhone:   body.PrimaryPhone,
			SignupSource:   body.SignupSource,
			FoundingUserID: p.UserID,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}

		// Link the user as owner of the new seller.
		if _, err := d.Identity.AddMember(r.Context(), p.UserID, s.ID,
			[]core.SellerRole{core.RoleOwner}); err != nil {
			writeError(w, r, err)
			return
		}

		// Re-issue the session scoped to the new seller.
		creds, err := d.Auth.IssueCredentials(r.Context(), p.UserID, &s.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"seller":     s,
			"token":      creds.Token,
			"expires_at": creds.ExpiresAt,
		})
	}
}

// MountOnboarding wires the onboarding routes.
//
// /v1/auth/dev-login is mounted UNCONDITIONALLY here but the handler returns
// 404 unless DevMode is true. That keeps the route table consistent across
// builds while making the endpoint inert in prod.
//
// /v1/auth/google/{start,callback} are mounted regardless and short-circuit
// to 503 when d.Google is nil.
func MountOnboarding(r chi.Router, d OnboardingDeps) {
	r.Post("/auth/dev-login", DevLoginHandler(d))
	r.Get("/auth/google/start", GoogleStartHandler(d))
	r.Get("/auth/google/callback", GoogleCallbackHandler(d))
}

// MountSellerProvisioning is mounted under /v1 (auth-required, NO seller scope).
func MountSellerProvisioning(r chi.Router, d OnboardingDeps) {
	r.Post("/sellers", ProvisionSellerHandler(d))
}

