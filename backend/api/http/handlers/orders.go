package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
)

// OrdersDeps are the dependencies for order handlers.
type OrdersDeps struct {
	Orders orders.Service
	Limits limits.Guard // optional; nil disables enforcement
}

func ListOrdersHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		result, err := d.Orders.List(r.Context(), orders.ListQuery{
			SellerID: p.SellerID, Limit: 50,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func CreateOrderHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())

		// Enforce daily order limit (contract-driven).
		if d.Limits != nil {
			if err := d.Limits.CheckOrderDay(r.Context(), p.SellerID); err != nil {
				if errors.Is(err, limits.ErrLimitExceeded) {
					writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
					return
				}
				writeError(w, r, err)
				return
			}
		}

		var req orders.CreateRequest
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		req.SellerID = p.SellerID
		order, err := d.Orders.Create(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

func GetOrderHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseOrderID(chi.URLParam(r, "orderID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		order, err := d.Orders.Get(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

func CancelOrderHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseOrderID(chi.URLParam(r, "orderID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		_ = decode(r, &req)
		if err := d.Orders.Cancel(r.Context(), p.SellerID, id, req.Reason); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

func MountOrders(r chi.Router, d OrdersDeps) {
	r.Get("/orders", ListOrdersHandler(d))
	r.Post("/orders", CreateOrderHandler(d))
	r.Get("/orders/{orderID}", GetOrderHandler(d))
	r.Post("/orders/{orderID}/cancel", CancelOrderHandler(d))
}
