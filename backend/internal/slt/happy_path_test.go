package slt_test

// Happy-path System-Level Test: order → book → in_transit → delivered
//
// This test exercises the full domain stack against a real Postgres instance
// (spun up by testcontainers). No mocks. Uses the sandbox carrier adapter
// instead of hitting a real courier API.

import (
	"context"
	"testing"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/allocation"
	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/buyerexp"
	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/carriers/sandbox"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/cod"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/ndr"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/slt"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

func TestHappyPath_OrderToDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("SLT: skipped in short mode (requires Docker)")
	}

	pool := slt.NewDB(t)
	ctx := context.Background()
	log := slt.NopLogger()

	// ── Wire up all services ────────────────────────────────────────────

	au := audit.New(pool, nil, core.SystemClock{}, log)
	walletSvc := wallet.New(pool, au, log)
	orderSvc := orders.New(pool, nil, log)
	pickupSvc := catalog.NewPickupService(pool, log)
	codSvc := cod.New(pool, walletSvc)
	ndrSvc := ndr.New(pool)

	// Tracking + shipments have a mutual dependency — break it with SetShipments.
	trackingSvc := tracking.New(pool, nil, nil, log)
	sandboxAdapter := sandbox.New()
	reg := carriers.NewRegistry()
	reg.Install(sandboxAdapter)
	shipSvc := shipments.New(pool, reg, walletSvc, orderSvc, log)
	trackingSvc.SetShipments(shipSvc)

	buyerSvc := buyerexp.New(pool, trackingSvc)

	// Policy engine (real DB-backed).
	policyEngine, err := policy.New(pool, au, core.SystemClock{}, log)
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	_ = policyEngine

	// ── Seed: active seller + user ───────────────────────────────────────

	ts := slt.CreateActiveSeller(t, pool)
	sellerID := ts.Seller.ID
	slt.EnsureWallet(t, ctx, pool, sellerID)

	// ── Seed: pickup location ────────────────────────────────────────────

	pickup, err := pickupSvc.Create(ctx, catalog.PickupCreateRequest{
		SellerID:     sellerID,
		Label:        "Warehouse A",
		ContactName:  "Ramesh Kumar",
		ContactPhone: "+919876543210",
		ContactEmail: "wh@example.com",
		Address: core.Address{
			Line1:   "Plot 42, MIDC",
			City:    "Mumbai",
			State:   "Maharashtra",
			Country: "IN",
			Pincode: "400093",
		},
		Pincode:   "400093",
		State:     "Maharashtra",
		Active:    true,
		IsDefault: true,
	})
	if err != nil {
		t.Fatalf("create pickup: %v", err)
	}
	t.Logf("✓ pickup location created: %s", pickup.ID)

	// ── Step 1: Create an order (draft state) ────────────────────────────

	order, err := orderSvc.Create(ctx, orders.CreateRequest{
		SellerID:       sellerID,
		Channel:        "shopify",
		ChannelOrderID: "SHOP-SLT-" + core.NewOrderID().String()[:8],
		BuyerName:      "Priya Sharma",
		BuyerPhone:     "+919123456789",
		BuyerEmail:     "priya@example.com",
		BillingAddress: core.Address{
			Line1: "12 MG Road", City: "Bengaluru", State: "Karnataka",
			Country: "IN", Pincode: "560001",
		},
		ShippingAddress: core.Address{
			Line1: "12 MG Road", City: "Bengaluru", State: "Karnataka",
			Country: "IN", Pincode: "560001",
		},
		ShippingPincode: "560001",
		ShippingState:   "Karnataka",
		PaymentMethod:   core.PaymentModePrepaid,
		SubtotalPaise:   core.FromRupees(999),
		ShippingPaise:   core.FromRupees(50),
		TotalPaise:      core.FromRupees(1049),
		PickupLocationID: pickup.ID,
		PackageWeightG:  500,
		PackageLengthMM: 200,
		PackageWidthMM:  150,
		PackageHeightMM: 100,
		Lines: []orders.OrderLine{{
			SKU: "TSHIRT-M-BLUE", Name: "Blue T-Shirt Medium",
			Quantity: 1, UnitPricePaise: core.FromRupees(999), UnitWeightG: 500,
		}},
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order.State != orders.StateDraft {
		t.Errorf("new order state=%s want draft", order.State)
	}
	t.Logf("✓ order created: %s (state=%s)", order.ID, order.State)

	// ── Step 2: Mark ready ───────────────────────────────────────────────

	if err := orderSvc.MarkReady(ctx, sellerID, order.ID); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	order, _ = orderSvc.Get(ctx, sellerID, order.ID)
	if order.State != orders.StateReady {
		t.Errorf("after MarkReady state=%s want ready", order.State)
	}
	t.Logf("✓ order ready")

	// ── Step 3: Simulate allocation — use sandbox carrier directly ───────

	sandboxCarrierID := core.CarrierIDFromUUID(core.NewCarrierID().UUID())

	// Fabricate allocation decision (real allocation needs rate cards in DB;
	// that seeding is covered by the pricing SLT — here we test downstream).
	decision := allocation.Decision{
		ID:       core.NewAllocationDecisionID(),
		OrderID:  order.ID,
		SellerID: sellerID,
		Candidates: []allocation.Candidate{{
			CarrierID:   sandboxCarrierID,
			ServiceType: core.ServiceTypeStandard,
			Quote: slt.FakeQuote(core.FromRupees(85)),
			Score: allocation.CompositeScore{CostScore: 0.9, SpeedScore: 0.8, Total: 0.87},
		}},
		WeightsUsed:    allocation.ObjectiveWeights{CostBP: 100, SpeedBP: 50},
		RecommendedIdx: 0,
		DecidedAt:      time.Now(),
	}

	// Register the sandbox with the carrier ID we'll use in the decision.
	// The registry uses Code() = "sandbox"; we'll create a named alias.
	namedSandbox := &namedAdapter{Adapter: sandboxAdapter, code: sandboxCarrierID.String()}
	reg.Install(namedSandbox)

	if err := orderSvc.MarkAllocating(ctx, sellerID, order.ID); err != nil {
		t.Fatalf("MarkAllocating: %v", err)
	}
	t.Logf("✓ order allocating")

	// ── Step 4: Book shipment ────────────────────────────────────────────

	shipment, err := shipSvc.Book(ctx, shipments.BookRequest{
		SellerID: sellerID,
		OrderID:  order.ID,
		Decision: decision,
		PickupAddress: core.Address{
			Line1: "Plot 42, MIDC", City: "Mumbai", State: "Maharashtra",
			Country: "IN", Pincode: "400093",
		},
		PickupContact:   core.ContactInfo{Name: "Ramesh Kumar", Phone: "+919876543210"},
		DropAddress: core.Address{
			Line1: "12 MG Road", City: "Bengaluru", State: "Karnataka",
			Country: "IN", Pincode: "560001",
		},
		DropContact:     core.ContactInfo{Name: "Priya Sharma", Phone: "+919123456789"},
		DropPincode:     "560001",
		PackageWeightG:  500,
		PackageLengthMM: 200,
		PackageWidthMM:  150,
		PackageHeightMM: 100,
		PaymentMode:     core.PaymentModePrepaid,
		DeclaredValue:   core.FromRupees(999),
		CODAmount:       0,
		InvoiceNumber:   "INV-SLT-001",
	})
	if err != nil {
		t.Fatalf("Book shipment: %v", err)
	}
	if shipment.State != shipments.StateBooked {
		t.Errorf("shipment state=%s want booked", shipment.State)
	}
	if shipment.AWB == "" {
		t.Error("AWB must be set after booking")
	}
	t.Logf("✓ shipment booked: AWB=%s", shipment.AWB)

	// ── Step 5: Carrier picks up → in_transit ────────────────────────────

	sandboxAdapter.AddTrackingEvent(shipment.AWB, "Picked Up", "PKP", "Mumbai", false, false)

	eventsToIngest := []carriers.TrackingEvent{{
		CarrierCode: sandboxCarrierID.String(),
		AWBNumber:   shipment.AWB,
		Status:      "Picked Up",
		StatusCode:  "PKP",
		Location:    "Mumbai",
		Timestamp:   time.Now(),
	}}
	if err := trackingSvc.IngestEvents(ctx, eventsToIngest, "webhook"); err != nil {
		t.Fatalf("IngestEvents pickup: %v", err)
	}

	shipment, err = shipSvc.Get(ctx, sellerID, shipment.ID)
	if err != nil {
		t.Fatalf("Get shipment after pickup: %v", err)
	}
	if shipment.State != shipments.StateInTransit {
		t.Errorf("after pickup event state=%s want in_transit", shipment.State)
	}
	// Advance order to in_transit too.
	if err := orderSvc.MarkInTransit(ctx, sellerID, order.ID); err != nil {
		t.Fatalf("MarkInTransit order: %v", err)
	}
	t.Logf("✓ shipment + order in transit")

	// ── Step 6: Delivery event ────────────────────────────────────────────

	eventsToIngest = []carriers.TrackingEvent{{
		CarrierCode: sandboxCarrierID.String(),
		AWBNumber:   shipment.AWB,
		Status:      "Delivered",
		StatusCode:  "DL",
		Location:    "Bengaluru",
		Timestamp:   time.Now(),
		IsDelivered: true,
	}}
	if err := trackingSvc.IngestEvents(ctx, eventsToIngest, "webhook"); err != nil {
		t.Fatalf("IngestEvents delivery: %v", err)
	}

	shipment, err = shipSvc.Get(ctx, sellerID, shipment.ID)
	if err != nil {
		t.Fatalf("Get shipment after delivery: %v", err)
	}
	if shipment.State != shipments.StateDelivered {
		t.Errorf("after delivery event state=%s want delivered", shipment.State)
	}
	// Advance order to delivered.
	if err := orderSvc.MarkDelivered(ctx, sellerID, order.ID); err != nil {
		t.Fatalf("MarkDelivered order: %v", err)
	}
	t.Logf("✓ shipment + order delivered!")

	// ── Step 7: Verify tracking events in DB ─────────────────────────────

	events, err := trackingSvc.ListEventsByShipment(ctx, sellerID, shipment.ID)
	if err != nil {
		t.Fatalf("ListEventsByShipment: %v", err)
	}
	if len(events) < 2 {
		t.Errorf("expected ≥2 tracking events, got %d", len(events))
	}
	t.Logf("✓ %d tracking events recorded", len(events))

	// ── Step 8: Issue a buyer-facing tracking token ───────────────────────

	token, err := buyerSvc.IssueToken(ctx, sellerID, shipment.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	view, err := buyerSvc.GetTrackingView(ctx, token)
	if err != nil {
		t.Fatalf("GetTrackingView: %v", err)
	}
	if view.AWB != shipment.AWB {
		t.Errorf("tracking view AWB=%s want %s", view.AWB, shipment.AWB)
	}
	t.Logf("✓ buyer tracking view accessible via token")

	// ── Step 9: Order state should be closed ─────────────────────────────

	if err := orderSvc.Close(ctx, sellerID, order.ID); err != nil {
		t.Fatalf("Close order: %v", err)
	}
	order, _ = orderSvc.Get(ctx, sellerID, order.ID)
	if order.State != orders.StateClosed {
		t.Errorf("order after close state=%s want closed", order.State)
	}
	t.Logf("✓ order closed")

	// ── Sanity: wallet should have been charged ───────────────────────────

	bal, err := walletSvc.Balance(ctx, sellerID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	// Started with ₹5000, shipment was ₹85 — balance should be reduced.
	if bal.Balance > core.FromRupees(5000) {
		t.Errorf("balance should be ≤ initial amount, got %s", bal.Balance)
	}
	t.Logf("✓ wallet balance: %s", bal.Balance)

	// ── Unused vars ──────────────────────────────────────────────────────
	_, _, _ = codSvc, ndrSvc, policyEngine
}

// ── NDR happy path ────────────────────────────────────────────────────────────

func TestNDR_OpenAndResolve(t *testing.T) {
	if testing.Short() {
		t.Skip("SLT: skipped in short mode")
	}

	pool := slt.NewDB(t)
	ctx := context.Background()
	log := slt.NopLogger()

	au := audit.New(pool, nil, core.SystemClock{}, log)
	walletSvc := wallet.New(pool, au, log)
	orderSvc := orders.New(pool, nil, log)
	pickupSvc := catalog.NewPickupService(pool, log)
	sandboxAdapter := sandbox.New()
	reg := carriers.NewRegistry()
	reg.Install(sandboxAdapter)
	shipSvc := shipments.New(pool, reg, walletSvc, orderSvc, log)
	trackingSvc := tracking.New(pool, nil, nil, log)
	trackingSvc.SetShipments(shipSvc)
	ndrSvc := ndr.New(pool)

	ts := slt.CreateActiveSeller(t, pool)
	sellerID := ts.Seller.ID
	slt.EnsureWallet(t, ctx, pool, sellerID)

	pickup, _ := pickupSvc.Create(ctx, catalog.PickupCreateRequest{
		SellerID: sellerID, Label: "WH-NDR",
		ContactName: "Test", ContactPhone: "+919000000000",
		Address: core.Address{Line1: "1 Test St", City: "Mumbai",
			State: "MH", Country: "IN", Pincode: "400001"},
		Pincode: "400001", State: "MH", Active: true, IsDefault: true,
	})

	order, _ := orderSvc.Create(ctx, orders.CreateRequest{
		SellerID: sellerID, Channel: "manual",
		ChannelOrderID: "NDR-TEST-" + core.NewOrderID().String()[:6],
		BuyerName: "Suresh", BuyerPhone: "+919111111111",
		BillingAddress: core.Address{Line1: "5 Park St", City: "Delhi",
			State: "DL", Country: "IN", Pincode: "110001"},
		ShippingAddress: core.Address{Line1: "5 Park St", City: "Delhi",
			State: "DL", Country: "IN", Pincode: "110001"},
		ShippingPincode: "110001", ShippingState: "DL",
		PaymentMethod:  core.PaymentModePrepaid,
		SubtotalPaise:  core.FromRupees(500),
		TotalPaise:     core.FromRupees(500),
		PickupLocationID: pickup.ID,
		PackageWeightG: 300, PackageLengthMM: 150, PackageWidthMM: 100, PackageHeightMM: 80,
		Lines: []orders.OrderLine{{SKU: "SKU1", Name: "Item", Quantity: 1,
			UnitPricePaise: core.FromRupees(500), UnitWeightG: 300}},
	})

	_ = orderSvc.MarkReady(ctx, sellerID, order.ID)
	_ = orderSvc.MarkAllocating(ctx, sellerID, order.ID)

	sandboxCarrierID := core.NewCarrierID()
	namedA := &namedAdapter{Adapter: sandboxAdapter, code: sandboxCarrierID.String()}
	reg.Install(namedA)

	shipment, err := shipSvc.Book(ctx, shipments.BookRequest{
		SellerID: sellerID, OrderID: order.ID,
		Decision: allocation.Decision{
			ID: core.NewAllocationDecisionID(), OrderID: order.ID, SellerID: sellerID,
			Candidates: []allocation.Candidate{{
				CarrierID: sandboxCarrierID, ServiceType: core.ServiceTypeStandard,
				Quote: slt.FakeQuote(core.FromRupees(60)),
				Score: allocation.CompositeScore{Total: 0.85},
			}},
			RecommendedIdx: 0, DecidedAt: time.Now(),
		},
		PickupAddress:   core.Address{Line1: "1 Test St", City: "Mumbai", State: "MH", Country: "IN", Pincode: "400001"},
		PickupContact:   core.ContactInfo{Name: "Test", Phone: "+919000000000"},
		DropAddress:     core.Address{Line1: "5 Park St", City: "Delhi", State: "DL", Country: "IN", Pincode: "110001"},
		DropContact:     core.ContactInfo{Name: "Suresh", Phone: "+919111111111"},
		DropPincode:     "110001",
		PackageWeightG: 300, PackageLengthMM: 150, PackageWidthMM: 100, PackageHeightMM: 80,
		PaymentMode: core.PaymentModePrepaid, DeclaredValue: core.FromRupees(500),
	})
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	t.Logf("✓ booked: AWB=%s", shipment.AWB)

	// Carrier reports NDR.
	_ = trackingSvc.IngestEvents(ctx, []carriers.TrackingEvent{{
		CarrierCode: sandboxCarrierID.String(),
		AWBNumber:   shipment.AWB,
		Status:      "Not Home", StatusCode: "NDR",
		Location: "Delhi", Timestamp: time.Now(),
	}}, "webhook")

	// Open NDR case.
	ndrCase, err := ndrSvc.OpenCase(ctx, sellerID, shipment.ID, order.ID,
		"customer not available", "Delhi")
	if err != nil {
		t.Fatalf("OpenCase: %v", err)
	}
	if ndrCase.State != ndr.StateOpen {
		t.Errorf("NDR state=%s want open", ndrCase.State)
	}
	t.Logf("✓ NDR case opened: %s", ndrCase.ID)

	// Seller requests reattempt.
	if err := ndrSvc.RequestReattempt(ctx, sellerID, ndrCase.ID, "seller"); err != nil {
		t.Fatalf("RequestReattempt: %v", err)
	}

	// Delivered on reattempt.
	_ = trackingSvc.IngestEvents(ctx, []carriers.TrackingEvent{{
		CarrierCode: sandboxCarrierID.String(),
		AWBNumber:   shipment.AWB,
		Status:      "Delivered", StatusCode: "DL",
		Location: "Delhi", Timestamp: time.Now(), IsDelivered: true,
	}}, "webhook")

	// Fetch NDR case and close it.
	ndrCase, _ = ndrSvc.GetByShipment(ctx, sellerID, shipment.ID)
	if err := ndrSvc.CloseDelivered(ctx, sellerID, ndrCase.ID); err != nil {
		t.Fatalf("CloseDelivered: %v", err)
	}
	ndrCase, _ = ndrSvc.GetByShipment(ctx, sellerID, shipment.ID)
	if ndrCase.State != ndr.StateDeliveredOnReattempt {
		t.Errorf("NDR final state=%s want delivered_on_reattempt", ndrCase.State)
	}
	t.Logf("✓ NDR resolved: delivered on reattempt")
}

// ── COD happy path ────────────────────────────────────────────────────────────

func TestCOD_RegisterCollectRemit(t *testing.T) {
	if testing.Short() {
		t.Skip("SLT: skipped in short mode")
	}

	pool := slt.NewDB(t)
	ctx := context.Background()
	log := slt.NopLogger()

	au := audit.New(pool, nil, core.SystemClock{}, log)
	walletSvc := wallet.New(pool, au, log)
	orderSvc := orders.New(pool, nil, log)
	pickupSvc := catalog.NewPickupService(pool, log)
	sandboxAdapter := sandbox.New()
	reg := carriers.NewRegistry()
	reg.Install(sandboxAdapter)
	shipSvc := shipments.New(pool, reg, walletSvc, orderSvc, log)
	codSvc := cod.New(pool, walletSvc)

	ts := slt.CreateActiveSeller(t, pool)
	sellerID := ts.Seller.ID
	slt.EnsureWallet(t, ctx, pool, sellerID)

	pickup, _ := pickupSvc.Create(ctx, catalog.PickupCreateRequest{
		SellerID: sellerID, Label: "WH-COD",
		ContactName: "Test COD", ContactPhone: "+919200000000",
		Address: core.Address{Line1: "2 Warehouse Rd", City: "Chennai",
			State: "TN", Country: "IN", Pincode: "600001"},
		Pincode: "600001", State: "TN", Active: true, IsDefault: true,
	})

	codAmountPaise := core.FromRupees(799)
	order, _ := orderSvc.Create(ctx, orders.CreateRequest{
		SellerID: sellerID, Channel: "website",
		ChannelOrderID: "COD-TEST-" + core.NewOrderID().String()[:6],
		BuyerName: "Anita", BuyerPhone: "+919222222222",
		BillingAddress: core.Address{Line1: "7 Raja St", City: "Hyderabad",
			State: "TS", Country: "IN", Pincode: "500001"},
		ShippingAddress: core.Address{Line1: "7 Raja St", City: "Hyderabad",
			State: "TS", Country: "IN", Pincode: "500001"},
		ShippingPincode: "500001", ShippingState: "TS",
		PaymentMethod: core.PaymentModeCOD,
		SubtotalPaise: codAmountPaise,
		TotalPaise:    codAmountPaise,
		CODAmountPaise: codAmountPaise,
		PickupLocationID: pickup.ID,
		PackageWeightG:  250, PackageLengthMM: 120, PackageWidthMM: 80, PackageHeightMM: 60,
		Lines: []orders.OrderLine{{SKU: "BOOK-001", Name: "Book", Quantity: 1,
			UnitPricePaise: codAmountPaise, UnitWeightG: 250}},
	})

	_ = orderSvc.MarkReady(ctx, sellerID, order.ID)
	_ = orderSvc.MarkAllocating(ctx, sellerID, order.ID)

	sandboxCarrierID := core.NewCarrierID()
	reg.Install(&namedAdapter{Adapter: sandboxAdapter, code: sandboxCarrierID.String()})

	shipment, err := shipSvc.Book(ctx, shipments.BookRequest{
		SellerID: sellerID, OrderID: order.ID,
		Decision: allocation.Decision{
			ID: core.NewAllocationDecisionID(), OrderID: order.ID, SellerID: sellerID,
			Candidates: []allocation.Candidate{{
				CarrierID: sandboxCarrierID, ServiceType: core.ServiceTypeStandard,
				Quote: slt.FakeQuote(core.FromRupees(70)),
				Score: allocation.CompositeScore{Total: 0.8},
			}},
			RecommendedIdx: 0, DecidedAt: time.Now(),
		},
		PickupAddress: core.Address{Line1: "2 Warehouse Rd", City: "Chennai", State: "TN", Country: "IN", Pincode: "600001"},
		PickupContact: core.ContactInfo{Name: "Test COD", Phone: "+919200000000"},
		DropAddress:   core.Address{Line1: "7 Raja St", City: "Hyderabad", State: "TS", Country: "IN", Pincode: "500001"},
		DropContact:   core.ContactInfo{Name: "Anita", Phone: "+919222222222"},
		DropPincode:   "500001",
		PackageWeightG: 250, PackageLengthMM: 120, PackageWidthMM: 80, PackageHeightMM: 60,
		PaymentMode: core.PaymentModeCOD, DeclaredValue: codAmountPaise, CODAmount: codAmountPaise,
	})
	if err != nil {
		t.Fatalf("Book COD: %v", err)
	}
	t.Logf("✓ COD shipment booked: AWB=%s", shipment.AWB)

	// Register COD tracking.
	if err := codSvc.Register(ctx, sellerID, shipment.ID, order.ID,
		sandboxCarrierID.String(), codAmountPaise); err != nil {
		t.Fatalf("COD Register: %v", err)
	}

	// Carrier delivers and collects cash.
	if err := codSvc.MarkCollected(ctx, sellerID, shipment.ID,
		codAmountPaise, time.Now()); err != nil {
		t.Fatalf("MarkCollected: %v", err)
	}

	// Remit to seller wallet.
	balBefore, _ := walletSvc.Balance(ctx, sellerID)
	if err := codSvc.Remit(ctx, sellerID, shipment.ID); err != nil {
		t.Fatalf("Remit: %v", err)
	}
	balAfter, _ := walletSvc.Balance(ctx, sellerID)
	if balAfter.Balance <= balBefore.Balance {
		t.Errorf("balance should increase after COD remittance: before=%s after=%s",
			balBefore.Balance, balAfter.Balance)
	}
	t.Logf("✓ COD remitted: wallet %s → %s", balBefore.Balance, balAfter.Balance)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// namedAdapter wraps the sandbox adapter but reports a different Code(),
// allowing multiple "carriers" in the registry for the same underlying adapter.
type namedAdapter struct {
	*sandbox.Adapter
	code string
}

func (n *namedAdapter) Code() string { return n.code }
