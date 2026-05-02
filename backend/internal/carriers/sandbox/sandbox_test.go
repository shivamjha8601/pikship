package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

func TestBook_success(t *testing.T) {
	a := New()
	r := a.Book(context.Background(), carriers.BookRequest{
		ShipmentID:  core.NewShipmentID(),
		OrderID:     core.NewOrderID(),
		PaymentMode: core.PaymentModePrepaid,
	})
	if !r.OK() {
		t.Fatalf("Book failed: %v", r.Err)
	}
	if r.Value.AWBNumber == "" {
		t.Error("AWBNumber must not be empty")
	}
}

func TestBook_uniqueAWBs(t *testing.T) {
	a := New()
	r1 := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	r2 := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	if r1.Value.AWBNumber == r2.Value.AWBNumber {
		t.Error("consecutive bookings must produce unique AWBs")
	}
}

func TestBook_simulateFailure(t *testing.T) {
	a := New()
	a.SimulateFailure()
	r := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	if r.OK() {
		t.Error("expected failure after SimulateFailure()")
	}
	if r.ErrClass != carriers.ErrClassTransient {
		t.Errorf("error class should be transient, got %s", r.ErrClass)
	}
	// Next call should succeed (failure is one-shot).
	r2 := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	if !r2.OK() {
		t.Errorf("failure should be one-shot, next call failed: %v", r2.Err)
	}
}

func TestCancel_knownAWB(t *testing.T) {
	a := New()
	r := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	cr := a.Cancel(context.Background(), carriers.CancelRequest{AWBNumber: r.Value.AWBNumber})
	if !cr.OK() || !cr.Value.Cancelled {
		t.Errorf("cancel known AWB failed: %v", cr.Err)
	}
}

func TestCancel_unknownAWB(t *testing.T) {
	a := New()
	cr := a.Cancel(context.Background(), carriers.CancelRequest{AWBNumber: "NONEXISTENT"})
	if cr.OK() {
		t.Error("cancel unknown AWB should fail")
	}
	if cr.ErrClass != carriers.ErrClassPermanent {
		t.Errorf("expected permanent error, got %s", cr.ErrClass)
	}
}

func TestFetchLabel(t *testing.T) {
	a := New()
	r := a.FetchLabel(context.Background(), carriers.LabelRequest{AWBNumber: "ANY"})
	if !r.OK() {
		t.Fatalf("FetchLabel failed: %v", r.Err)
	}
	if len(r.Value.Data) == 0 {
		t.Error("label data must not be empty")
	}
}

func TestAddTrackingEvent_thenFetch(t *testing.T) {
	a := New()
	br := a.Book(context.Background(), carriers.BookRequest{ShipmentID: core.NewShipmentID()})
	awb := br.Value.AWBNumber

	a.AddTrackingEvent(awb, "Out for Delivery", "OFD", "Mumbai", false, false)
	a.AddTrackingEvent(awb, "Delivered", "DL", "Mumbai", true, false)

	since := time.Now().Add(-1 * time.Hour)
	r := a.FetchTrackingEvents(context.Background(), awb, since)
	if !r.OK() {
		t.Fatalf("FetchTrackingEvents failed: %v", r.Err)
	}
	// booked event + OFD + DL = 3
	if len(r.Value) < 3 {
		t.Errorf("expected ≥3 events, got %d", len(r.Value))
	}
	last := r.Value[len(r.Value)-1]
	if !last.IsDelivered {
		t.Error("last event should be delivered")
	}
}

func TestCapabilities(t *testing.T) {
	a := New()
	caps := a.Capabilities()
	if !caps.SupportsCOD {
		t.Error("sandbox must support COD")
	}
	if len(caps.Services) == 0 {
		t.Error("sandbox must expose at least one service type")
	}
}

func TestCheckServiceability(t *testing.T) {
	a := New()
	ok, err := a.CheckServiceability(context.Background(), carriers.ServiceabilityQuery{
		PickupPincode: "400001",
		ShipToPincode: "110001",
	})
	if err != nil || !ok {
		t.Errorf("sandbox should always be serviceable, err=%v ok=%v", err, ok)
	}
}
