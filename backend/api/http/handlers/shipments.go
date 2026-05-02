package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/reports"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// ShipmentDeps are the dependencies for shipment + wallet handlers.
type ShipmentDeps struct {
	Shipments shipments.Service
	Wallet    wallet.Service
	Reports   reports.Service
}

func GetShipmentHandler(d ShipmentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		sh, err := d.Shipments.Get(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sh)
	}
}

func CancelShipmentHandler(d ShipmentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		var req struct{ Reason string `json:"reason"` }
		_ = decode(r, &req)
		if err := d.Shipments.Cancel(r.Context(), p.SellerID, id, req.Reason); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

func GetWalletBalanceHandler(d ShipmentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		bal, err := d.Wallet.Balance(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, bal)
	}
}

func GetShipmentSummaryHandler(d ShipmentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		q := r.URL.Query()
		from, _ := time.Parse(time.DateOnly, q.Get("from"))
		to, _ := time.Parse(time.DateOnly, q.Get("to"))
		if from.IsZero() {
			from = time.Now().AddDate(0, -1, 0)
		}
		if to.IsZero() {
			to = time.Now()
		}
		sum, err := d.Reports.ShipmentSummary(r.Context(), p.SellerID, from, to)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sum)
	}
}

func MountShipments(r chi.Router, d ShipmentDeps) {
	r.Get("/shipments/{shipmentID}", GetShipmentHandler(d))
	r.Post("/shipments/{shipmentID}/cancel", CancelShipmentHandler(d))
	r.Get("/wallet/balance", GetWalletBalanceHandler(d))
	r.Get("/reports/shipments/summary", GetShipmentSummaryHandler(d))
}
