package slt

import (
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/pricing"
)

// FakeQuote builds a minimal pricing.Quote with the given total cost.
// Use in SLTs to seed allocation.Candidate without real rate cards in DB.
func FakeQuote(totalPaise core.Paise) pricing.Quote {
	return pricing.Quote{
		TotalPaise:    totalPaise,
		EstimatedDays: 3,
		Zone:          "Z1",
		Breakdown:     map[string]core.Paise{"base": totalPaise},
	}
}
