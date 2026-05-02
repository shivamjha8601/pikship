package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
)

// SellerDeps are the dependencies for seller handlers.
type SellerDeps struct {
	Seller seller.Service
}

func GetSellerHandler(d SellerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		s, err := d.Seller.Get(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

func GetKYCHandler(d SellerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		kyc, err := d.Seller.GetKYC(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, kyc)
	}
}

func SubmitKYCHandler(d SellerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var app seller.KYCApplication
		if err := decode(r, &app); err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		if err := d.Seller.SubmitKYC(r.Context(), p.SellerID, app); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "submitted"})
	}
}

func MountSeller(r chi.Router, d SellerDeps) {
	r.Get("/seller", GetSellerHandler(d))
	r.Get("/seller/kyc", GetKYCHandler(d))
	r.Post("/seller/kyc", SubmitKYCHandler(d))
}
