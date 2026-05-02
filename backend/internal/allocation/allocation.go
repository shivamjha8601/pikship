// Package allocation is the carrier picker. It scores all eligible carriers
// for an order using configurable objective weights (cost, speed, reliability,
// seller-preference) and returns a ranked Decision.
//
// Per LLD §03-services/07-allocation.
package allocation

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
	"github.com/vishal1132/pikshipp/backend/internal/pricing"
)

// Engine is the public API.
type Engine interface {
	Allocate(ctx context.Context, in AllocateInput) (Decision, error)
}

// AllocateInput is one order's allocation request.
type AllocateInput struct {
	SellerID        core.SellerID
	OrderID         core.OrderID
	PickupPincode   core.Pincode
	ShipToPincode   core.Pincode
	DeclaredWeightG int
	LengthMM        int
	WidthMM         int
	HeightMM        int
	PaymentMode     core.PaymentMode
	DeclaredValue   core.Paise
}

// Decision is the ranked output.
type Decision struct {
	ID             core.AllocationDecisionID
	OrderID        core.OrderID
	SellerID       core.SellerID
	Candidates     []Candidate
	WeightsUsed    ObjectiveWeights
	RecommendedIdx int
	DecidedAt      time.Time
}

// Candidate is a scored (carrier, service) option.
type Candidate struct {
	CarrierID   core.CarrierID
	ServiceType core.ServiceType
	Quote       pricing.Quote
	Score       CompositeScore
}

// CompositeScore breaks down the objective weights.
type CompositeScore struct {
	CostScore    float64
	SpeedScore   float64
	Total        float64
}

// ObjectiveWeights carries per-objective weights in basis points.
type ObjectiveWeights struct {
	CostBP    int64
	SpeedBP   int64
}

// FilterReason explains why a carrier was excluded.
type FilterReason string

const (
	FilterNotAllowed    FilterReason = "not_in_allowed_set"
	FilterExcluded      FilterReason = "seller_excluded"
	FilterNoRateCard    FilterReason = "no_rate_card"
	FilterNoPincoverage FilterReason = "no_pin_coverage"
)

// engine implementation ---

type engine struct {
	pricing pricing.Engine
	policy  policy.Engine
	log     *slog.Logger
}

// New constructs the allocation engine.
func New(pricingEngine pricing.Engine, policyEngine policy.Engine, log *slog.Logger) Engine {
	return &engine{pricing: pricingEngine, policy: policyEngine, log: log}
}

func (e *engine) Allocate(ctx context.Context, in AllocateInput) (Decision, error) {
	// Resolve which carriers are allowed for this seller.
	allowedVal, err := e.policy.Resolve(ctx, in.SellerID, policy.KeyCarriersAllowedSet)
	if err != nil {
		return Decision{}, fmt.Errorf("allocation.Allocate: policy allowed: %w", err)
	}
	allowedSet, err := allowedVal.AsStringSet()
	if err != nil {
		return Decision{}, fmt.Errorf("allocation.Allocate: parse allowed set: %w", err)
	}

	excludedVal, err := e.policy.Resolve(ctx, in.SellerID, policy.KeyCarriersExcludedSet)
	if err != nil {
		return Decision{}, fmt.Errorf("allocation.Allocate: policy excluded: %w", err)
	}
	excludedSet, _ := excludedVal.AsStringSet()

	// Read objective weights from policy.
	weights := ObjectiveWeights{CostBP: 100, SpeedBP: 50}
	if wv, err := e.policy.Resolve(ctx, in.SellerID, policy.KeyAllocationWeightCost); err == nil {
		weights.CostBP, _ = wv.AsInt64()
	}
	if wv, err := e.policy.Resolve(ctx, in.SellerID, policy.KeyAllocationWeightSpeed); err == nil {
		weights.SpeedBP, _ = wv.AsInt64()
	}

	// Build candidates from the allowed set.
	var pricingCandidates []pricing.CarrierCandidate
	for carrier := range allowedSet {
		if excludedSet.Has(carrier) {
			continue
		}
		cid, err := core.ParseCarrierID(carrier)
		if err != nil {
			continue
		}
		// Try both standard and express.
		for _, svc := range []core.ServiceType{core.ServiceTypeStandard, core.ServiceTypeExpress} {
			pricingCandidates = append(pricingCandidates, pricing.CarrierCandidate{
				CarrierID:   cid,
				ServiceType: svc,
			})
		}
	}

	quotes, err := e.pricing.QuoteAll(ctx, pricing.QuoteAllInput{
		SellerID:        in.SellerID,
		PickupPincode:   in.PickupPincode,
		ShipToPincode:   in.ShipToPincode,
		DeclaredWeightG: in.DeclaredWeightG,
		LengthMM:        in.LengthMM,
		WidthMM:         in.WidthMM,
		HeightMM:        in.HeightMM,
		PaymentMode:     in.PaymentMode,
		DeclaredValue:   in.DeclaredValue,
		Candidates:      pricingCandidates,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("allocation.Allocate: quote all: %w", err)
	}

	if len(quotes) == 0 {
		return Decision{}, fmt.Errorf("allocation.Allocate: no carriers available")
	}

	// Score each quote.
	// Normalise cost: cheapest = 1.0, most expensive = 0.0.
	minCost, maxCost := int64(quotes[0].TotalPaise), int64(quotes[0].TotalPaise)
	maxDays := quotes[0].EstimatedDays
	for _, q := range quotes[1:] {
		if int64(q.TotalPaise) < minCost {
			minCost = int64(q.TotalPaise)
		}
		if int64(q.TotalPaise) > maxCost {
			maxCost = int64(q.TotalPaise)
		}
		if q.EstimatedDays > maxDays {
			maxDays = q.EstimatedDays
		}
	}

	candidates := make([]Candidate, len(quotes))
	for i, q := range quotes {
		var costScore, speedScore float64
		costRange := maxCost - minCost
		if costRange > 0 {
			costScore = 1.0 - float64(int64(q.TotalPaise)-minCost)/float64(costRange)
		} else {
			costScore = 1.0
		}
		if maxDays > 0 {
			speedScore = 1.0 - float64(q.EstimatedDays-1)/float64(maxDays)
		} else {
			speedScore = 1.0
		}
		totalBP := float64(weights.CostBP + weights.SpeedBP)
		total := (costScore*float64(weights.CostBP) + speedScore*float64(weights.SpeedBP)) / totalBP
		candidates[i] = Candidate{
			CarrierID:   q.CarrierID,
			ServiceType: q.ServiceType,
			Quote:       q,
			Score:       CompositeScore{CostScore: costScore, SpeedScore: speedScore, Total: total},
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score.Total > candidates[j].Score.Total
	})

	return Decision{
		ID:             core.AllocationDecisionID(core.NewAllocationDecisionID()),
		OrderID:        in.OrderID,
		SellerID:       in.SellerID,
		Candidates:     candidates,
		WeightsUsed:    weights,
		RecommendedIdx: 0,
		DecidedAt:      time.Now(),
	}, nil
}
