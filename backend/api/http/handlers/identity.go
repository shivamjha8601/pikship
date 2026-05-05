package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
)

// IdentityDeps are the dependencies for identity handlers.
type IdentityDeps struct {
	Identity identity.Service
	Auth     auth.Authenticator
}

// MeHandler returns the current user's profile.
//
// active_seller_id is the seller bound to the current session token (empty
// string if none). The frontend uses this to know whether it needs to call
// /v1/auth/select-seller before hitting any seller-scoped endpoint.
func MeHandler(d IdentityDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		user, err := d.Identity.GetUser(r.Context(), p.UserID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		sellers, _ := d.Identity.ListUserSellers(r.Context(), p.UserID)
		var activeSellerID string
		if !p.SellerID.IsZero() {
			activeSellerID = p.SellerID.String()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user":             user,
			"sellers":          sellers,
			"active_seller_id": activeSellerID,
		})
	}
}

// SelectSellerHandler switches the active seller for the session.
func SelectSellerHandler(d IdentityDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var req struct {
			SellerID string `json:"seller_id"`
		}
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		sellerID, err := core.ParseSellerID(req.SellerID)
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		membership, err := d.Identity.SelectSeller(r.Context(), p.UserID, sellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		// Issue new credentials with the selected seller.
		creds, err := d.Auth.IssueCredentials(r.Context(), p.UserID, &sellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token":      creds.Token,
			"expires_at": creds.ExpiresAt,
			"membership": membership,
		})
	}
}

// LogoutHandler revokes the current session.
func LogoutHandler(d IdentityDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if len(token) > 7 {
			token = token[7:]
		}
		_ = d.Auth.Revoke(r.Context(), token)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// InviteHandler sends an invitation to join a seller.
func InviteHandler(d IdentityDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var req struct {
			Email string             `json:"email"`
			Roles []core.SellerRole  `json:"roles"`
		}
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		inv, err := d.Identity.InviteUserToSeller(r.Context(), p.SellerID, p.UserID, identity.InviteInput{
			Email: req.Email, Roles: req.Roles,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, inv)
	}
}

// MountIdentity mounts identity routes onto a chi router.
func MountIdentity(r chi.Router, d IdentityDeps) {
	r.Get("/me", MeHandler(d))
	r.Post("/auth/select-seller", SelectSellerHandler(d))
	r.Post("/auth/logout", LogoutHandler(d))
	r.Post("/sellers/invites", InviteHandler(d))
}
