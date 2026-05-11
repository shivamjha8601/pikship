package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/pricing"
)

// PricingDeps are the dependencies for pricing handlers.
type PricingDeps struct {
	Engine pricing.Engine
}

// PackageDims is one package in a multi-package quote request.
type PackageDims struct {
	WeightG  int `json:"weight_g"`
	LengthMM int `json:"length_mm"`
	WidthMM  int `json:"width_mm"`
	HeightMM int `json:"height_mm"`
}

// QuoteRequest is the body of POST /v1/pricing/quote.
type QuoteRequest struct {
	PickupPincode core.Pincode     `json:"pickup_pincode"`
	ShipToPincode core.Pincode     `json:"ship_to_pincode"`
	PaymentMode   core.PaymentMode `json:"payment_mode,omitempty"`
	DeclaredValue core.Paise       `json:"declared_value_paise,omitempty"`
	Packages      []PackageDims    `json:"packages"`
}

// CarrierQuote is one row of the response.
type CarrierQuote struct {
	CarrierID     string                `json:"carrier_id"`
	CarrierCode   string                `json:"carrier_code"`
	ServiceType   core.ServiceType      `json:"service_type"`
	EstimatedDays int                   `json:"estimated_days"`
	TotalPaise    core.Paise            `json:"total_paise"`
	Zone          string                `json:"zone,omitempty"`
	Packages      int                   `json:"packages"`
	Breakdown     map[string]core.Paise `json:"breakdown,omitempty"`
}

// QuoteResponse is the body of POST /v1/pricing/quote.
type QuoteResponse struct {
	Quotes []CarrierQuote `json:"quotes"`
}

// QuoteHandler returns shipping quotes across all known carriers for the
// given pickup/ship pincodes + packages. Multi-package: each package is
// priced independently then summed per (carrier, service).
func QuoteHandler(d PricingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var req QuoteRequest
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		if !req.PickupPincode.IsValid() || !req.ShipToPincode.IsValid() {
			writeError(w, r, fmt.Errorf("invalid pincode: %w", core.ErrInvalidArgument))
			return
		}
		if len(req.Packages) == 0 {
			writeError(w, r, fmt.Errorf("at least one package required: %w", core.ErrInvalidArgument))
			return
		}
		mode := req.PaymentMode
		if mode == "" {
			mode = core.PaymentModePrepaid
		}
		if !mode.IsValid() {
			writeError(w, r, fmt.Errorf("invalid payment_mode: %w", core.ErrInvalidArgument))
			return
		}

		// Known carriers to price. Today only Delhivery is seeded; once more
		// adapters land they'll add their (carrier, service) candidates here.
		candidates := []pricing.CarrierCandidate{
			{CarrierID: carriers.DelhiveryCarrierID, ServiceType: core.ServiceTypeStandard},
		}

		// Sum per (carrier_id, service_type).
		type key struct {
			cid core.CarrierID
			svc core.ServiceType
		}
		sums := make(map[key]*CarrierQuote)

		for _, pkg := range req.Packages {
			if pkg.WeightG < 1 || pkg.LengthMM < 1 || pkg.WidthMM < 1 || pkg.HeightMM < 1 {
				writeError(w, r, fmt.Errorf("package dimensions must be >0: %w", core.ErrInvalidArgument))
				return
			}
			quotes, err := d.Engine.QuoteAll(r.Context(), pricing.QuoteAllInput{
				SellerID:        p.SellerID,
				PickupPincode:   req.PickupPincode,
				ShipToPincode:   req.ShipToPincode,
				DeclaredWeightG: pkg.WeightG,
				LengthMM:        pkg.LengthMM,
				WidthMM:         pkg.WidthMM,
				HeightMM:        pkg.HeightMM,
				PaymentMode:     mode,
				DeclaredValue:   req.DeclaredValue,
				Candidates:      candidates,
			})
			if err != nil {
				writeError(w, r, err)
				return
			}
			for _, q := range quotes {
				k := key{cid: q.CarrierID, svc: q.ServiceType}
				if cur, ok := sums[k]; ok {
					cur.TotalPaise += q.TotalPaise
					cur.Packages++
					for kk, vv := range q.Breakdown {
						cur.Breakdown[kk] += vv
					}
					if q.EstimatedDays > cur.EstimatedDays {
						cur.EstimatedDays = q.EstimatedDays
					}
				} else {
					sums[k] = &CarrierQuote{
						CarrierID:     q.CarrierID.String(),
						CarrierCode:   carrierCodeFor(q.CarrierID),
						ServiceType:   q.ServiceType,
						EstimatedDays: q.EstimatedDays,
						TotalPaise:    q.TotalPaise,
						Zone:          q.Zone,
						Packages:      1,
						Breakdown:     cloneBreakdown(q.Breakdown),
					}
				}
			}
		}

		out := QuoteResponse{Quotes: make([]CarrierQuote, 0, len(sums))}
		for _, v := range sums {
			out.Quotes = append(out.Quotes, *v)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func carrierCodeFor(id core.CarrierID) string {
	switch id {
	case carriers.DelhiveryCarrierID:
		return "delhivery"
	default:
		return ""
	}
}

func cloneBreakdown(m map[string]core.Paise) map[string]core.Paise {
	out := make(map[string]core.Paise, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MountPricing registers pricing routes under the authenticated, seller-scoped
// group.
func MountPricing(r chi.Router, d PricingDeps) {
	r.Post("/pricing/quote", QuoteHandler(d))
}
