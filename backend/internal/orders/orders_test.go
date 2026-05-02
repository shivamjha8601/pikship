package orders

import "testing"

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to OrderState
		want     bool
	}{
		{StateDraft, StateReady, true},
		{StateDraft, StateCancelled, true},
		{StateDraft, StateBooked, false},     // not a valid jump
		{StateReady, StateAllocating, true},
		{StateReady, StateDraft, true},        // re-draft allowed
		{StateAllocating, StateBooked, true},
		{StateAllocating, StateCancelled, true},
		{StateBooked, StateInTransit, true},
		{StateBooked, StateRTO, true},
		{StateInTransit, StateDelivered, true},
		{StateInTransit, StateRTO, true},
		{StateDelivered, StateClosed, true},
		{StateDelivered, StateRTO, true},
		{StateClosed, StateDraft, false},       // terminal
		{StateCancelled, StateReady, false},    // terminal
		{StateRTO, StateClosed, true},
		{StateRTO, StateDraft, false},
	}
	for _, tc := range cases {
		got := CanTransition(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("CanTransition(%s→%s)=%v want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestOrderState_constants(t *testing.T) {
	states := []OrderState{
		StateDraft, StateReady, StateAllocating, StateBooked,
		StateInTransit, StateDelivered, StateClosed, StateCancelled, StateRTO,
	}
	seen := map[OrderState]bool{}
	for _, s := range states {
		if seen[s] {
			t.Errorf("duplicate state: %s", s)
		}
		seen[s] = true
	}
	if len(seen) != 9 {
		t.Errorf("expected 9 states, got %d", len(seen))
	}
}
