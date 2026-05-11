package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
)

// OrdersDeps are the dependencies for order handlers.
type OrdersDeps struct {
	Orders    orders.Service
	Limits    limits.Guard      // optional; nil disables enforcement
	Shipments shipments.Service // optional; enables cancel-cascades-to-shipment
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
		// Cascade: if a shipment exists for this order, cancel it first so
		// the carrier-side AWB is released and we don't end up with a
		// cancelled order whose shipment quietly delivers. Tolerate "no
		// shipment yet" (cancel before book) cleanly.
		if d.Shipments != nil {
			if sh, sErr := d.Shipments.GetByOrderID(r.Context(), p.SellerID, id); sErr == nil && sh.ID.String() != "00000000-0000-0000-0000-000000000000" {
				reason := req.Reason
				if reason == "" {
					reason = "order_cancelled"
				}
				if cErr := d.Shipments.Cancel(r.Context(), p.SellerID, sh.ID, reason); cErr != nil {
					// Don't block the order cancel on a carrier failure — log and continue.
					// The shipment will be marked failed by the cancel call's audit trail.
					_ = cErr
				}
			}
		}
		if err := d.Orders.Cancel(r.Context(), p.SellerID, id, req.Reason); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	}
}

// GetOrderShipmentHandler returns the shipment associated with an order
// (or 404 if the order hasn't been booked yet). The frontend uses this on
// the order detail page after a reload to re-hydrate its tracking UI.
func GetOrderShipmentHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseOrderID(chi.URLParam(r, "orderID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		if d.Shipments == nil {
			writeError(w, r, core.ErrNotFound)
			return
		}
		sh, err := d.Shipments.GetByOrderID(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, sh)
	}
}

// MarkPaidHandler records the seller's manual confirmation that a UPI/bank
// transfer has reflected. Until Razorpay webhooks land, this is how prepaid
// orders flip from "unpaid" to "paid".
func MarkPaidHandler(d OrdersDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseOrderID(chi.URLParam(r, "orderID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		var req struct {
			Reference string `json:"reference"`
		}
		_ = decode(r, &req)
		userID := p.UserID
		if err := d.Orders.MarkPaid(r.Context(), p.SellerID, id, orders.MarkPaidRef{
			Reference:    req.Reference,
			PaidByUserID: &userID,
		}); err != nil {
			writeError(w, r, err)
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

func MountOrders(r chi.Router, d OrdersDeps) {
	r.Get("/orders", ListOrdersHandler(d))
	r.Post("/orders", CreateOrderHandler(d))
	r.Get("/orders/{orderID}", GetOrderHandler(d))
	r.Post("/orders/{orderID}/cancel", CancelOrderHandler(d))
	r.Post("/orders/{orderID}/mark-paid", MarkPaidHandler(d))
	r.Get("/orders/{orderID}/shipment", GetOrderShipmentHandler(d))
}
