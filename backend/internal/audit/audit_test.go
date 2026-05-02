package audit

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// --- IsHighValue ---

func TestIsHighValue_knownActions(t *testing.T) {
	cases := []struct {
		action string
		want   bool
	}{
		// prefix-matched ("wallet." covers all wallet movements)
		{"wallet.funds_added", true},
		{"wallet.hold_created", true},
		{"wallet.charge_committed", true},
		// exact matches from HighValueActions
		{"cod.remitted", true},
		{"seller.suspended", true},
		{"seller.reactivated", true},
		{"seller.wound_down", true},
		{"user.role_granted", true},
		{"user.role_revoked", true},
		{"user.locked", true},
		{"ops.manual_adjustment", true},
		{"ops.kyc_override", true},
		{"ops.cross_seller_view", true},
		{"policy.lock_set", true},
		{"policy.lock_removed", true},
		{"contract.signed", true},
		{"contract.terminated", true},
		{"contract.amended", true},
		{"carrier.credential_rotated", true},
		// prefix-matched ("seller.kyc_" prefix)
		{"seller.kyc_approved", true},
		{"seller.kyc_rejected", true},
		// prefix-matched ("weight_dispute.")
		{"weight_dispute.opened", true},
		// not high-value
		{"shipment.status_updated", false},
		{"seller.created", false},
		{"user.password_changed", false},
		{"", false},
	}
	for _, tc := range cases {
		got := IsHighValue(tc.action)
		if got != tc.want {
			t.Errorf("IsHighValue(%q) = %v, want %v", tc.action, got, tc.want)
		}
	}
}

// --- computeEventHash determinism ---

func makeEvent(action string) Event {
	sid := core.SellerIDFromUUID(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	return Event{
		ID:       core.AuditEventIDFromUUID(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")),
		SellerID: &sid,
		Actor:    Actor{Kind: ActorSellerUser, Ref: "u1"},
		Action:   action,
		Target:   Target{Kind: "seller", Ref: "s1"},
		Payload:  map[string]any{"k": "v"},
		OccurredAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestComputeEventHash_deterministic(t *testing.T) {
	e := makeEvent("seller.created")
	h1 := computeEventHash(e, "")
	h2 := computeEventHash(e, "")
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Fatal("hash must not be empty")
	}
}

func TestComputeEventHash_prevHashChains(t *testing.T) {
	e := makeEvent("seller.created")
	h0 := computeEventHash(e, "")
	h1 := computeEventHash(e, "someprev")
	if h0 == h1 {
		t.Fatal("different prevHash must produce different event hash")
	}
}

// --- VerifyChain ---

func buildChain(n int) ([]Event, []string) {
	events := make([]Event, n)
	hashes := make([]string, n)
	prev := ""
	for i := range n {
		e := makeEvent("seller.created")
		// give each event a unique ID and time to differentiate
		e.ID = core.AuditEventIDFromUUID(uuid.New())
		e.OccurredAt = e.OccurredAt.Add(time.Duration(i) * time.Second)
		h := computeEventHash(e, prev)
		events[i] = e
		hashes[i] = h
		prev = h
	}
	return events, hashes
}

func TestVerifyChain_clean(t *testing.T) {
	events, hashes := buildChain(5)
	if err := VerifyChain(events, hashes); err != nil {
		t.Fatalf("expected clean chain, got: %v", err)
	}
}

func TestVerifyChain_tampered_hash(t *testing.T) {
	events, hashes := buildChain(5)
	hashes[2] = "tampered"
	if err := VerifyChain(events, hashes); err == nil {
		t.Fatal("expected error for tampered hash")
	}
}

func TestVerifyChain_tampered_event(t *testing.T) {
	events, hashes := buildChain(5)
	// mutate an event field without touching the stored hash
	events[3].Action = "seller.terminated"
	if err := VerifyChain(events, hashes); err == nil {
		t.Fatal("expected error for tampered event")
	}
}

func TestVerifyChain_empty(t *testing.T) {
	if err := VerifyChain(nil, nil); err != nil {
		t.Fatalf("empty chain should pass: %v", err)
	}
}

func TestVerifyChain_length_mismatch(t *testing.T) {
	events, hashes := buildChain(3)
	if err := VerifyChain(events, hashes[:2]); err == nil {
		t.Fatal("expected error for length mismatch")
	}
}
