// Package sandbox provides an in-memory carrier adapter for tests and
// local development. It simulates booking, tracking, and NDR without
// hitting any real carrier API.
package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Adapter is the in-memory sandbox carrier.
type Adapter struct {
	mu        sync.Mutex
	bookings  map[string]*carriers.BookResponse // awb → response
	events    map[string][]carriers.TrackingEvent
	awbSeq    int
	failNext  bool // set to true to simulate a failure on the next Book call
}

// New creates a new Sandbox adapter.
func New() *Adapter {
	return &Adapter{
		bookings: make(map[string]*carriers.BookResponse),
		events:   make(map[string][]carriers.TrackingEvent),
	}
}

func (a *Adapter) Code() string        { return "sandbox" }
func (a *Adapter) DisplayName() string { return "Sandbox (Test)" }
func (a *Adapter) Capabilities() carriers.Capabilities {
	return carriers.Capabilities{
		Services:              []core.ServiceType{core.ServiceTypeStandard, core.ServiceTypeExpress},
		SupportsCOD:           true,
		MaxDeclaredValuePaise: core.FromRupees(100_000),
		MaxWeightG:            30_000,
		SupportsNDRActions:    true,
		SupportsLabelFetch:    true,
	}
}

func (a *Adapter) CheckServiceability(_ context.Context, _ carriers.ServiceabilityQuery) (bool, error) {
	return true, nil
}

// SimulateFailure causes the next Book call to return a transient error.
func (a *Adapter) SimulateFailure() { a.mu.Lock(); a.failNext = true; a.mu.Unlock() }

func (a *Adapter) Book(_ context.Context, req carriers.BookRequest) carriers.Result[carriers.BookResponse] {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failNext {
		a.failNext = false
		return carriers.Result[carriers.BookResponse]{
			Err:      fmt.Errorf("sandbox: simulated transient failure"),
			ErrClass: carriers.ErrClassTransient,
		}
	}
	a.awbSeq++
	awb := fmt.Sprintf("SBOX%010d", a.awbSeq)
	resp := &carriers.BookResponse{AWBNumber: awb, CarrierShipmentRef: awb}
	a.bookings[awb] = resp
	// Seed initial event.
	a.events[awb] = []carriers.TrackingEvent{{
		AWBNumber: awb, Status: "booked", StatusCode: "BK",
		Location: "Origin", Timestamp: time.Now(),
	}}
	return carriers.Result[carriers.BookResponse]{Value: *resp}
}

func (a *Adapter) Cancel(_ context.Context, req carriers.CancelRequest) carriers.Result[carriers.CancelResponse] {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.bookings[req.AWBNumber]; !ok {
		return carriers.Result[carriers.CancelResponse]{
			Err:      fmt.Errorf("sandbox: awb %s not found", req.AWBNumber),
			ErrClass: carriers.ErrClassPermanent,
		}
	}
	delete(a.bookings, req.AWBNumber)
	return carriers.Result[carriers.CancelResponse]{Value: carriers.CancelResponse{Cancelled: true}}
}

func (a *Adapter) FetchLabel(_ context.Context, req carriers.LabelRequest) carriers.Result[carriers.LabelResponse] {
	return carriers.Result[carriers.LabelResponse]{
		Value: carriers.LabelResponse{Format: "pdf", Data: []byte("SANDBOX_LABEL")},
	}
}

func (a *Adapter) FetchTrackingEvents(_ context.Context, awb string, since time.Time) carriers.Result[[]carriers.TrackingEvent] {
	a.mu.Lock()
	defer a.mu.Unlock()
	all := a.events[awb]
	var filtered []carriers.TrackingEvent
	for _, e := range all {
		if !e.Timestamp.Before(since) {
			filtered = append(filtered, e)
		}
	}
	return carriers.Result[[]carriers.TrackingEvent]{Value: filtered}
}

func (a *Adapter) RaiseNDRAction(_ context.Context, req carriers.NDRActionRequest) carriers.Result[carriers.NDRActionResponse] {
	return carriers.Result[carriers.NDRActionResponse]{
		Value: carriers.NDRActionResponse{Accepted: true, Message: "sandbox: NDR action accepted"},
	}
}

// AddTrackingEvent injects a tracking event (for test control).
func (a *Adapter) AddTrackingEvent(awb string, status, code, location string, delivered, rto bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events[awb] = append(a.events[awb], carriers.TrackingEvent{
		AWBNumber:   awb,
		Status:      status,
		StatusCode:  code,
		Location:    location,
		Timestamp:   time.Now(),
		IsDelivered: delivered,
		IsRTO:       rto,
	})
}
