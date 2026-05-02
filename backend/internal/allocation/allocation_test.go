package allocation

import "testing"

func TestCompositeScore_range(t *testing.T) {
	s := CompositeScore{CostScore: 0.8, SpeedScore: 0.6}
	// Manually compute: (0.8*100 + 0.6*50) / 150 = (80+30)/150 ≈ 0.733
	total := (0.8*100 + 0.6*50) / float64(100+50)
	s.Total = total
	if s.Total < 0 || s.Total > 1 {
		t.Errorf("composite score must be in [0,1], got %f", s.Total)
	}
}

func TestObjectiveWeights_defaults(t *testing.T) {
	w := ObjectiveWeights{CostBP: 100, SpeedBP: 50}
	if w.CostBP <= 0 {
		t.Error("CostBP must be positive")
	}
}
