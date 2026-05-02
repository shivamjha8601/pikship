package risk

import (
	"context"
	"log/slog"
	"io"
	"testing"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

func nopLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEngine_noEvaluators(t *testing.T) {
	e := New(nil, nopLog())
	s := e.Score(context.Background(), Input{
		OrderID:  core.NewOrderID(),
		SellerID: core.NewSellerID(),
	})
	if s.Action != ActionAllow {
		t.Errorf("empty evaluators should always allow, got %s", s.Action)
	}
	if s.Total != 0 {
		t.Errorf("empty evaluators score should be 0, got %f", s.Total)
	}
}

func TestCODHighValueEvaluator_below(t *testing.T) {
	ev := &CODHighValueEvaluator{ThresholdPaise: core.FromRupees(500)}
	sig := ev.Evaluate(context.Background(), Input{
		PaymentMode: core.PaymentModeCOD,
		CODAmount:   core.FromRupees(200), // below threshold
	})
	if sig.Value != 0 {
		t.Errorf("below threshold should give score 0, got %f", sig.Value)
	}
}

func TestCODHighValueEvaluator_above(t *testing.T) {
	ev := &CODHighValueEvaluator{ThresholdPaise: core.FromRupees(500)}
	sig := ev.Evaluate(context.Background(), Input{
		PaymentMode: core.PaymentModeCOD,
		CODAmount:   core.FromRupees(1000), // 2x threshold
	})
	if sig.Value <= 0 {
		t.Errorf("above threshold should give positive score, got %f", sig.Value)
	}
	if sig.Value > 1 {
		t.Errorf("score must be ≤1, got %f", sig.Value)
	}
}

func TestCODHighValueEvaluator_prepaid(t *testing.T) {
	ev := &CODHighValueEvaluator{ThresholdPaise: core.FromRupees(500)}
	sig := ev.Evaluate(context.Background(), Input{
		PaymentMode: core.PaymentModePrepaid,
		CODAmount:   core.FromRupees(10000), // prepaid — should not trigger
	})
	if sig.Value != 0 {
		t.Errorf("prepaid should never trigger COD evaluator, got %f", sig.Value)
	}
}

func TestEngine_blocksHighRisk(t *testing.T) {
	// Two evaluators that both return max score.
	type alwaysMax struct{}
	type maxEval struct{ alwaysMax }
	_ = maxEval{}

	// Use the real evaluators at extreme values.
	ev := &CODHighValueEvaluator{ThresholdPaise: 1} // 1 paise threshold = everything is high value
	e := New([]Evaluator{ev}, nopLog())
	s := e.Score(context.Background(), Input{
		PaymentMode: core.PaymentModeCOD,
		CODAmount:   core.FromRupees(100_000), // ₹1L = very high
	})
	if s.Action == ActionAllow {
		t.Errorf("expected block or review for extreme COD, got allow")
	}
}

func TestEngine_compositeScore(t *testing.T) {
	evs := []Evaluator{
		&CODHighValueEvaluator{ThresholdPaise: core.FromRupees(500)},
		&HighWeightEvaluator{ThresholdG: 10000},
	}
	e := New(evs, nopLog())

	// Low-risk input: prepaid, normal weight
	s := e.Score(context.Background(), Input{
		PaymentMode: core.PaymentModePrepaid,
		WeightG:     500,
	})
	if s.Action != ActionAllow {
		t.Errorf("low risk should allow, got %s", s.Action)
	}
	if len(s.Signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(s.Signals))
	}
}
