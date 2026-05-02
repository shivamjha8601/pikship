package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/contracts"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
)

// AdminDeps wires the services for operator-facing endpoints.
type AdminDeps struct {
	Seller    seller.Service
	Contracts contracts.Service
	Limits    limits.Guard
}

// UpgradeToEnterpriseHandler atomically:
// 1. Changes seller_type → enterprise (or whatever type the body specifies)
// 2. Creates a new contract with the supplied terms
// 3. Activates the contract — which writes seller-level policy overrides
//
// Body:
//
//	{
//	  "new_type": "enterprise",
//	  "terms": {
//	    "policy_overrides": {
//	      "limits.shipments_per_month": 0,
//	      "wallet.credit_limit_inr": 50000000,
//	      "features.insurance": true
//	    },
//	    "monthly_minimum_paise": 100000000,
//	    "sla_delivered_p95_days": 3
//	  },
//	  "rate_card_id": "<uuid>"  // optional
//	}
func UpgradeToEnterpriseHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		// In a real deployment this is gated by RequireRole(operator); for
		// the v0 build any authenticated user can call it. Will be tightened
		// once the ops user kind has live UX.

		sellerID, err := core.ParseSellerID(chi.URLParam(r, "sellerID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}

		var body struct {
			NewType    string         `json:"new_type"`
			Terms      map[string]any `json:"terms"`
			RateCardID string         `json:"rate_card_id,omitempty"`
		}
		if err := decode(r, &body); err != nil {
			writeError(w, r, err)
			return
		}
		if body.NewType == "" {
			body.NewType = string(core.SellerTypeEnterprise)
		}

		// Step 1: change seller_type.
		if err := d.Seller.ChangeType(r.Context(), sellerID, core.SellerType(body.NewType), p.UserID); err != nil {
			writeError(w, r, err)
			return
		}

		// Step 2: create contract.
		var rcPtr *core.RateCardID
		if body.RateCardID != "" {
			rc, err := core.ParseRateCardID(body.RateCardID)
			if err == nil {
				rcPtr = &rc
			}
		}
		contract, err := d.Contracts.Create(r.Context(), sellerID, body.Terms, rcPtr,
			time.Now(), p.UserID)
		if err != nil {
			writeError(w, r, err)
			return
		}

		// Step 3: activate contract — applies policy overrides.
		if err := d.Contracts.Activate(r.Context(), sellerID, contract.ID, p.UserID); err != nil {
			writeError(w, r, err)
			return
		}

		// Re-fetch so the response reflects activated state.
		active, _ := d.Contracts.GetActive(r.Context(), sellerID)
		writeJSON(w, http.StatusOK, map[string]any{
			"seller_id":   sellerID,
			"new_type":    body.NewType,
			"contract":    active,
		})
	}
}

// UsageHandler returns the seller's current usage vs limits — used by the
// frontend to show "X / Y shipments this month" widgets.
func UsageHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		u, err := d.Limits.Usage(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	}
}

// ActiveContractHandler returns the seller's currently-active contract.
func ActiveContractHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		c, err := d.Contracts.GetActive(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

// ListContractsHandler returns all contracts (active + history).
func ListContractsHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		list, err := d.Contracts.List(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// MountAdmin mounts /v1/admin/* (operator-only in real deployments) and
// the seller-scoped contract/usage views under /v1.
func MountAdmin(r chi.Router, d AdminDeps) {
	// Operator-initiated upgrade. Auth required, no seller scope.
	r.Post("/admin/sellers/{sellerID}/upgrade", UpgradeToEnterpriseHandler(d))
}

// MountSellerContractViews mounts read-only contract + usage endpoints
// inside the seller-scoped router.
func MountSellerContractViews(r chi.Router, d AdminDeps) {
	r.Get("/seller/contract", ActiveContractHandler(d))
	r.Get("/seller/contracts", ListContractsHandler(d))
	r.Get("/seller/usage", UsageHandler(d))
}
