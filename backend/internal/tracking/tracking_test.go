package tracking

import (
	"testing"
	"time"
)

func TestNormalise_known(t *testing.T) {
	cases := []struct {
		code, text string
		want       CanonicalStatus
	}{
		{"DL", "", StatusDelivered},
		{"DLVD", "", StatusDelivered},
		{"OFD", "", StatusOutForDel},
		{"PKP", "", StatusPickedUp},
		{"IT", "", StatusInTransit},
		{"RTO", "", StatusRTO},
		{"RTO-DL", "", StatusRTODeliv},
		// text-based fallbacks
		{"UNKNOWN", "delivered successfully", StatusDelivered},
		{"UNKNOWN", "out for delivery route", StatusOutForDel},
		{"UNKNOWN", "picked up from seller", StatusPickedUp},
		{"UNKNOWN", "in transit to hub", StatusInTransit},
		{"UNKNOWN", "rto initiated today", StatusRTO},
		{"UNKNOWN", "totally unknown event xyz", StatusException},
	}
	for _, tc := range cases {
		got := normalise("delhivery", tc.code, tc.text)
		if got != tc.want {
			t.Errorf("normalise(code=%q text=%q) = %s, want %s",
				tc.code, tc.text, got, tc.want)
		}
	}
}

func TestDedupeHash_stable(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	h1 := dedupeHash("delhivery", "AWB123", "DL", ts)
	h2 := dedupeHash("delhivery", "AWB123", "DL", ts)
	if h1 != h2 {
		t.Errorf("dedupeHash not deterministic: %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Error("dedupeHash must not be empty")
	}
}

func TestDedupeHash_distinct(t *testing.T) {
	ts1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	// different AWB
	if dedupeHash("c", "awb1", "s", ts1) == dedupeHash("c", "awb2", "s", ts1) {
		t.Error("different AWBs must give different hashes")
	}
	// different timestamp
	if dedupeHash("c", "awb1", "s", ts1) == dedupeHash("c", "awb1", "s", ts2) {
		t.Error("different timestamps must give different hashes")
	}
	// different carrier
	if dedupeHash("carrier_a", "awb1", "s", ts1) == dedupeHash("carrier_b", "awb1", "s", ts1) {
		t.Error("different carriers must give different hashes")
	}
}

func TestContains(t *testing.T) {
	if !contains("Out For Delivery", "out for delivery") {
		t.Error("case-insensitive match failed")
	}
	if contains("Picked Up", "delivered") {
		t.Error("should not match unrelated substring")
	}
	if !contains("RTO INITIATED", "rto") {
		t.Error("all-caps should match lowercase sub")
	}
}
