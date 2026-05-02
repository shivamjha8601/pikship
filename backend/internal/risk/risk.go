// Package risk provides a pluggable signal evaluator framework for fraud
// detection and order risk scoring. Per LLD §03-services/24-risk.
package risk

import (
	"context"
	"log/slog"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Score is the composite risk score for one order.
type Score struct {
	OrderID    core.OrderID
	SellerID   core.SellerID
	Total      float64 // 0.0 (safe) to 1.0 (high risk)
	Signals    []Signal
	Action     Action
}

// Signal is one contributing risk factor.
type Signal struct {
	Name    string
	Value   float64
	Weight  float64
	Detail  string
}

// Action is the recommended action.
type Action string

const (
	ActionAllow  Action = "allow"
	ActionReview Action = "review"
	ActionBlock  Action = "block"
)

// Evaluator is one pluggable signal.
type Evaluator interface {
	Name() string
	Evaluate(ctx context.Context, input Input) Signal
}

// Input is the data available to evaluators.
type Input struct {
	SellerID      core.SellerID
	OrderID       core.OrderID
	BuyerPhone    string
	ShipToPincode core.Pincode
	CODAmount     core.Paise
	PaymentMode   core.PaymentMode
	WeightG       int
}

// Engine scores an order against all registered evaluators.
type Engine struct {
	evaluators []Evaluator
	log        *slog.Logger
}

// New constructs the risk engine with the given evaluators.
func New(evaluators []Evaluator, log *slog.Logger) *Engine {
	return &Engine{evaluators: evaluators, log: log}
}

// Score runs all evaluators and returns a composite score.
func (e *Engine) Score(ctx context.Context, input Input) Score {
	var signals []Signal
	var total float64
	var totalWeight float64

	for _, ev := range e.evaluators {
		sig := ev.Evaluate(ctx, input)
		signals = append(signals, sig)
		total += sig.Value * sig.Weight
		totalWeight += sig.Weight
	}

	if totalWeight > 0 {
		total /= totalWeight
	}

	action := ActionAllow
	switch {
	case total >= 0.8:
		action = ActionBlock
	case total >= 0.5:
		action = ActionReview
	}

	return Score{
		OrderID:  input.OrderID,
		SellerID: input.SellerID,
		Total:    total,
		Signals:  signals,
		Action:   action,
	}
}

// --- built-in evaluators ---

// CODHighValueEvaluator flags high-value COD orders.
type CODHighValueEvaluator struct {
	ThresholdPaise core.Paise
}

func (e *CODHighValueEvaluator) Name() string { return "cod_high_value" }
func (e *CODHighValueEvaluator) Evaluate(_ context.Context, in Input) Signal {
	if in.PaymentMode != core.PaymentModeCOD || in.CODAmount <= e.ThresholdPaise {
		return Signal{Name: e.Name(), Value: 0, Weight: 0.3}
	}
	score := float64(in.CODAmount-e.ThresholdPaise) / float64(e.ThresholdPaise)
	if score > 1 {
		score = 1
	}
	return Signal{Name: e.Name(), Value: score, Weight: 0.3, Detail: "COD amount exceeds threshold"}
}

// HighWeightEvaluator flags unusually heavy packages.
type HighWeightEvaluator struct {
	ThresholdG int
}

func (e *HighWeightEvaluator) Name() string { return "high_weight" }
func (e *HighWeightEvaluator) Evaluate(_ context.Context, in Input) Signal {
	if in.WeightG <= e.ThresholdG {
		return Signal{Name: e.Name(), Value: 0, Weight: 0.1}
	}
	return Signal{Name: e.Name(), Value: 0.5, Weight: 0.1, Detail: "Weight exceeds normal range"}
}
