package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/allocation"
	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
)

// BookingDeps wires the dependencies needed to take an order through the
// allocation → booking → tracking-schedule sequence.
type BookingDeps struct {
	Orders     orders.Service
	Allocation allocation.Engine
	Shipments  shipments.Service
	Pickup     catalog.PickupService
	Tracking   tracking.Service
}

// BookOrderHandler turns a created order into a real shipment: it allocates
// a carrier, calls the carrier adapter to obtain an AWB, and registers the
// resulting shipment for tracking polls.
func BookOrderHandler(d BookingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		orderID, err := core.ParseOrderID(chi.URLParam(r, "orderID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}

		order, err := d.Orders.Get(r.Context(), p.SellerID, orderID)
		if err != nil {
			writeError(w, r, err)
			return
		}

		// Drop-state guard. Booking only makes sense for orders that haven't
		// already booked. Cancelled / closed orders shouldn't book either.
		if order.AWBNumber != "" {
			writeError(w, r, fmt.Errorf("order already booked (awb=%s): %w", order.AWBNumber, core.ErrInvalidArgument))
			return
		}

		// The pickup location holds the from-pincode + contact info we'll
		// pass to the carrier adapter.
		pickup, err := d.Pickup.Get(r.Context(), p.SellerID, order.PickupLocationID)
		if err != nil {
			writeError(w, r, fmt.Errorf("pickup lookup: %w", err))
			return
		}

		// Mark the order ready (if it's a fresh draft) so MarkAllocating in
		// shipments.Book has a valid prior state to transition from.
		if order.State == orders.StateDraft {
			if err := d.Orders.MarkReady(r.Context(), p.SellerID, orderID); err != nil &&
				!errors.Is(err, core.ErrInvalidArgument) {
				writeError(w, r, fmt.Errorf("mark ready: %w", err))
				return
			}
		}

		decision, err := d.Allocation.Allocate(r.Context(), allocation.AllocateInput{
			SellerID:        p.SellerID,
			OrderID:         orderID,
			PickupPincode:   pickup.Pincode,
			ShipToPincode:   order.ShippingPincode,
			DeclaredWeightG: order.PackageWeightG,
			LengthMM:        order.PackageLengthMM,
			WidthMM:         order.PackageWidthMM,
			HeightMM:        order.PackageHeightMM,
			PaymentMode:     order.PaymentMethod,
			DeclaredValue:   order.SubtotalPaise,
		})
		if err != nil {
			writeError(w, r, fmt.Errorf("allocate: %w", err))
			return
		}

		ship, err := d.Shipments.Book(r.Context(), shipments.BookRequest{
			SellerID:         p.SellerID,
			OrderID:          orderID,
			PickupLocationID: order.PickupLocationID,
			Decision:         decision,
			PickupAddress:    pickup.Address,
			PickupContact:   core.ContactInfo{Name: pickup.ContactName, Phone: pickup.ContactPhone, Email: pickup.ContactEmail},
			DropAddress:     order.ShippingAddress,
			DropContact:     core.ContactInfo{Name: order.BuyerName, Phone: order.BuyerPhone, Email: order.BuyerEmail},
			DropPincode:     order.ShippingPincode,
			PackageWeightG:  order.PackageWeightG,
			PackageLengthMM: order.PackageLengthMM,
			PackageWidthMM:  order.PackageWidthMM,
			PackageHeightMM: order.PackageHeightMM,
			PaymentMode:     order.PaymentMethod,
			DeclaredValue:   order.SubtotalPaise,
			CODAmount:       order.CODAmountPaise,
			InvoiceNumber:   order.OrderRef,
			SellerReference: order.ChannelOrderID,
		})
		if err != nil {
			writeError(w, r, fmt.Errorf("book: %w", err))
			return
		}

		// Register this shipment for tracking polls. Best-effort: a poll-schedule
		// failure shouldn't fail the booking call, the AWB is already issued.
		if err := d.Tracking.SchedulePoll(r.Context(), p.SellerID, ship.ID, ship.AWB, ship.CarrierCode); err != nil {
			// log via response? for now we just continue
			_ = err
		}

		writeJSON(w, http.StatusCreated, ship)
	}
}

// RefreshTrackingHandler triggers a fresh pull of carrier tracking events
// for one shipment. Used by the UI's "refresh" button on the order detail
// page and as a manual fallback when the periodic poller isn't running.
func RefreshTrackingHandler(d BookingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		shipID, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		if err := d.Tracking.PollNow(r.Context(), p.SellerID, shipID); err != nil {
			writeError(w, r, err)
			return
		}
		events, err := d.Tracking.ListEventsByShipment(r.Context(), p.SellerID, shipID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}

// MountBooking registers the booking + tracking-refresh routes.
func MountBooking(r chi.Router, d BookingDeps) {
	r.Post("/orders/{orderID}/book", BookOrderHandler(d))
	r.Post("/shipments/{shipmentID}/refresh", RefreshTrackingHandler(d))
	r.Get("/shipments/{shipmentID}/tracking-events", ListShipmentEventsHandler(d))
}

// ListShipmentEventsHandler returns all tracking events for a shipment.
func ListShipmentEventsHandler(d BookingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		shipID, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		events, err := d.Tracking.ListEventsByShipment(r.Context(), p.SellerID, shipID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}
