package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/buyerexp"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/ndr"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
)

// TrackingDeps are the dependencies for tracking and NDR handlers.
type TrackingDeps struct {
	Tracking tracking.Service
	BuyerExp buyerexp.Service
	NDR      ndr.Service
}

func GetTrackingEventsHandler(d TrackingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		events, err := d.Tracking.ListEventsByShipment(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}

// PublicTrackHandler serves buyer-facing tracking (no auth).
func PublicTrackHandler(d TrackingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		view, err := d.BuyerExp.GetTrackingView(r.Context(), token)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// PublicTrackByAWBHandler serves buyer-facing tracking by AWB.
func PublicTrackByAWBHandler(d TrackingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		awb := chi.URLParam(r, "awb")
		view, err := d.BuyerExp.GetByAWB(r.Context(), awb)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

func GetNDRCaseHandler(d TrackingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParseShipmentID(chi.URLParam(r, "shipmentID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		c, err := d.NDR.GetByShipment(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func NDRActionHandler(d TrackingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		caseID, err := core.ParseNDRCaseID(chi.URLParam(r, "caseID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		var req struct {
			Action     string        `json:"action"`
			NewAddress *core.Address `json:"new_address,omitempty"`
		}
		if err := decode(r, &req); err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		switch req.Action {
		case "reattempt":
			err = d.NDR.RequestReattempt(r.Context(), p.SellerID, caseID, "seller")
		case "change_address":
			if req.NewAddress == nil {
				writeError(w, r, core.ErrInvalidArgument)
				return
			}
			err = d.NDR.RequestAddressChange(r.Context(), p.SellerID, caseID, *req.NewAddress, "seller")
		case "rto":
			err = d.NDR.InitiateRTO(r.Context(), p.SellerID, caseID, "", "seller")
		default:
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func MountTracking(r chi.Router, d TrackingDeps) {
	// Authenticated
	r.Get("/shipments/{shipmentID}/tracking", GetTrackingEventsHandler(d))
	r.Get("/shipments/{shipmentID}/ndr", GetNDRCaseHandler(d))
	r.Post("/ndr/{caseID}/action", NDRActionHandler(d))
}

func MountPublicTracking(r chi.Router, d TrackingDeps) {
	// No auth — buyer-facing
	r.Get("/track/{token}", PublicTrackHandler(d))
	r.Get("/track/awb/{awb}", PublicTrackByAWBHandler(d))
}
