package core

import (
	"database/sql/driver"
	"fmt"

	"github.com/google/uuid"
)

// Typed IDs. Each entity gets its own type so the compiler stops you
// from passing a SellerID where an OrderID is expected. DB type is UUID.

type (
	SellerID             uuid.UUID
	SubSellerID          uuid.UUID
	UserID               uuid.UUID
	OrderID              uuid.UUID
	ShipmentID           uuid.UUID
	CarrierID            uuid.UUID
	ChannelID            uuid.UUID
	PickupLocationID     uuid.UUID
	ProductID            uuid.UUID
	BuyerID              uuid.UUID
	AuditEventID         uuid.UUID
	HoldID               uuid.UUID
	LedgerEntryID        uuid.UUID
	AllocationDecisionID uuid.UUID
	SessionID            uuid.UUID
	OutboxEventID        uuid.UUID
	IdempotencyKeyID     uuid.UUID
	PolicyKeyID          uuid.UUID
	NDRCaseID            uuid.UUID
	ReturnID             uuid.UUID
	RTOID                uuid.UUID
	CODRemittanceID      uuid.UUID
	WeightDisputeID      uuid.UUID
	NotificationID       uuid.UUID
	TicketID             uuid.UUID
	OperatorActionID     uuid.UUID
	RiskDetectionID      uuid.UUID
	ContractID           uuid.UUID
	RateCardID           uuid.UUID
	ShipmentBookingID    uuid.UUID
	TrackingEventID      uuid.UUID
)

// The rest of this file is generated machinery: NewXID, String, IsZero,
// ParseXID, Value, Scan for each typed ID. Macro-style to keep the surface
// uniform — see LLD §01-core/02-ids for the rationale.

// --- SellerID ---

func NewSellerID() SellerID                  { return SellerID(uuid.New()) }
func (id SellerID) String() string           { return uuid.UUID(id).String() }
func (id SellerID) UUID() uuid.UUID          { return uuid.UUID(id) }
func (id SellerID) IsZero() bool             { return uuid.UUID(id) == uuid.Nil }
func SellerIDFromUUID(u uuid.UUID) SellerID  { return SellerID(u) }
func ParseSellerID(s string) (SellerID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return SellerID{}, fmt.Errorf("core.ParseSellerID: %w", err)
	}
	return SellerID(u), nil
}
func (id SellerID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *SellerID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- SubSellerID ---

func NewSubSellerID() SubSellerID                  { return SubSellerID(uuid.New()) }
func (id SubSellerID) String() string              { return uuid.UUID(id).String() }
func (id SubSellerID) UUID() uuid.UUID             { return uuid.UUID(id) }
func (id SubSellerID) IsZero() bool                { return uuid.UUID(id) == uuid.Nil }
func SubSellerIDFromUUID(u uuid.UUID) SubSellerID  { return SubSellerID(u) }
func (id SubSellerID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *SubSellerID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- UserID ---

func NewUserID() UserID                  { return UserID(uuid.New()) }
func (id UserID) String() string         { return uuid.UUID(id).String() }
func (id UserID) UUID() uuid.UUID        { return uuid.UUID(id) }
func (id UserID) IsZero() bool           { return uuid.UUID(id) == uuid.Nil }
func UserIDFromUUID(u uuid.UUID) UserID  { return UserID(u) }
func ParseUserID(s string) (UserID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return UserID{}, fmt.Errorf("core.ParseUserID: %w", err)
	}
	return UserID(u), nil
}
func (id UserID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *UserID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- OrderID ---

func NewOrderID() OrderID                  { return OrderID(uuid.New()) }
func (id OrderID) String() string          { return uuid.UUID(id).String() }
func (id OrderID) UUID() uuid.UUID         { return uuid.UUID(id) }
func (id OrderID) IsZero() bool            { return uuid.UUID(id) == uuid.Nil }
func OrderIDFromUUID(u uuid.UUID) OrderID  { return OrderID(u) }
func ParseOrderID(s string) (OrderID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return OrderID{}, fmt.Errorf("core.ParseOrderID: %w", err)
	}
	return OrderID(u), nil
}
func (id OrderID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *OrderID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ShipmentID ---

func NewShipmentID() ShipmentID                  { return ShipmentID(uuid.New()) }
func (id ShipmentID) String() string             { return uuid.UUID(id).String() }
func (id ShipmentID) UUID() uuid.UUID            { return uuid.UUID(id) }
func (id ShipmentID) IsZero() bool               { return uuid.UUID(id) == uuid.Nil }
func ShipmentIDFromUUID(u uuid.UUID) ShipmentID  { return ShipmentID(u) }
func ParseShipmentID(s string) (ShipmentID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return ShipmentID{}, fmt.Errorf("core.ParseShipmentID: %w", err)
	}
	return ShipmentID(u), nil
}
func (id ShipmentID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *ShipmentID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- CarrierID ---

func NewCarrierID() CarrierID                  { return CarrierID(uuid.New()) }
func (id CarrierID) String() string            { return uuid.UUID(id).String() }
func (id CarrierID) UUID() uuid.UUID           { return uuid.UUID(id) }
func (id CarrierID) IsZero() bool              { return uuid.UUID(id) == uuid.Nil }
func CarrierIDFromUUID(u uuid.UUID) CarrierID  { return CarrierID(u) }
func ParseCarrierID(s string) (CarrierID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return CarrierID{}, fmt.Errorf("core.ParseCarrierID: %w", err)
	}
	return CarrierID(u), nil
}
func (id CarrierID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *CarrierID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ChannelID ---

func NewChannelID() ChannelID                  { return ChannelID(uuid.New()) }
func (id ChannelID) String() string            { return uuid.UUID(id).String() }
func (id ChannelID) UUID() uuid.UUID           { return uuid.UUID(id) }
func (id ChannelID) IsZero() bool              { return uuid.UUID(id) == uuid.Nil }
func ChannelIDFromUUID(u uuid.UUID) ChannelID  { return ChannelID(u) }
func (id ChannelID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *ChannelID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- PickupLocationID ---

func NewPickupLocationID() PickupLocationID                  { return PickupLocationID(uuid.New()) }
func (id PickupLocationID) String() string                   { return uuid.UUID(id).String() }
func (id PickupLocationID) UUID() uuid.UUID                  { return uuid.UUID(id) }
func (id PickupLocationID) IsZero() bool                     { return uuid.UUID(id) == uuid.Nil }
func PickupLocationIDFromUUID(u uuid.UUID) PickupLocationID  { return PickupLocationID(u) }
func ParsePickupLocationID(s string) (PickupLocationID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return PickupLocationID{}, fmt.Errorf("core.ParsePickupLocationID: %w", err)
	}
	return PickupLocationID(u), nil
}
func (id PickupLocationID) Value() (driver.Value, error)     { return uuidValue(uuid.UUID(id)) }
func (id *PickupLocationID) Scan(src any) error              { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ProductID ---

func NewProductID() ProductID                  { return ProductID(uuid.New()) }
func (id ProductID) String() string            { return uuid.UUID(id).String() }
func (id ProductID) UUID() uuid.UUID           { return uuid.UUID(id) }
func (id ProductID) IsZero() bool              { return uuid.UUID(id) == uuid.Nil }
func ProductIDFromUUID(u uuid.UUID) ProductID  { return ProductID(u) }
func (id ProductID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *ProductID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- BuyerID ---

func NewBuyerID() BuyerID                  { return BuyerID(uuid.New()) }
func (id BuyerID) String() string          { return uuid.UUID(id).String() }
func (id BuyerID) UUID() uuid.UUID         { return uuid.UUID(id) }
func (id BuyerID) IsZero() bool            { return uuid.UUID(id) == uuid.Nil }
func BuyerIDFromUUID(u uuid.UUID) BuyerID  { return BuyerID(u) }
func (id BuyerID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *BuyerID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- AuditEventID ---

func NewAuditEventID() AuditEventID                  { return AuditEventID(uuid.New()) }
func (id AuditEventID) String() string               { return uuid.UUID(id).String() }
func (id AuditEventID) UUID() uuid.UUID              { return uuid.UUID(id) }
func (id AuditEventID) IsZero() bool                 { return uuid.UUID(id) == uuid.Nil }
func AuditEventIDFromUUID(u uuid.UUID) AuditEventID  { return AuditEventID(u) }
func (id AuditEventID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *AuditEventID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- HoldID ---

func NewHoldID() HoldID                  { return HoldID(uuid.New()) }
func (id HoldID) String() string         { return uuid.UUID(id).String() }
func (id HoldID) UUID() uuid.UUID        { return uuid.UUID(id) }
func (id HoldID) IsZero() bool           { return uuid.UUID(id) == uuid.Nil }
func HoldIDFromUUID(u uuid.UUID) HoldID  { return HoldID(u) }
func (id HoldID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *HoldID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- LedgerEntryID ---

func NewLedgerEntryID() LedgerEntryID                  { return LedgerEntryID(uuid.New()) }
func (id LedgerEntryID) String() string                { return uuid.UUID(id).String() }
func (id LedgerEntryID) UUID() uuid.UUID               { return uuid.UUID(id) }
func (id LedgerEntryID) IsZero() bool                  { return uuid.UUID(id) == uuid.Nil }
func LedgerEntryIDFromUUID(u uuid.UUID) LedgerEntryID  { return LedgerEntryID(u) }
func (id LedgerEntryID) Value() (driver.Value, error)  { return uuidValue(uuid.UUID(id)) }
func (id *LedgerEntryID) Scan(src any) error           { return uuidScan(src, (*uuid.UUID)(id)) }

// --- AllocationDecisionID ---

func NewAllocationDecisionID() AllocationDecisionID                  { return AllocationDecisionID(uuid.New()) }
func (id AllocationDecisionID) String() string                       { return uuid.UUID(id).String() }
func (id AllocationDecisionID) UUID() uuid.UUID                      { return uuid.UUID(id) }
func (id AllocationDecisionID) IsZero() bool                         { return uuid.UUID(id) == uuid.Nil }
func AllocationDecisionIDFromUUID(u uuid.UUID) AllocationDecisionID  { return AllocationDecisionID(u) }
func (id AllocationDecisionID) Value() (driver.Value, error)         { return uuidValue(uuid.UUID(id)) }
func (id *AllocationDecisionID) Scan(src any) error                  { return uuidScan(src, (*uuid.UUID)(id)) }

// --- SessionID ---

func NewSessionID() SessionID                  { return SessionID(uuid.New()) }
func (id SessionID) String() string            { return uuid.UUID(id).String() }
func (id SessionID) UUID() uuid.UUID           { return uuid.UUID(id) }
func (id SessionID) IsZero() bool              { return uuid.UUID(id) == uuid.Nil }
func SessionIDFromUUID(u uuid.UUID) SessionID  { return SessionID(u) }
func (id SessionID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *SessionID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- OutboxEventID ---

func NewOutboxEventID() OutboxEventID                  { return OutboxEventID(uuid.New()) }
func (id OutboxEventID) String() string                { return uuid.UUID(id).String() }
func (id OutboxEventID) UUID() uuid.UUID               { return uuid.UUID(id) }
func (id OutboxEventID) IsZero() bool                  { return uuid.UUID(id) == uuid.Nil }
func OutboxEventIDFromUUID(u uuid.UUID) OutboxEventID  { return OutboxEventID(u) }
func ParseOutboxEventID(s string) (OutboxEventID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return OutboxEventID{}, fmt.Errorf("core.ParseOutboxEventID: %w", err)
	}
	return OutboxEventID(u), nil
}
func (id OutboxEventID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *OutboxEventID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- IdempotencyKeyID ---

func NewIdempotencyKeyID() IdempotencyKeyID                  { return IdempotencyKeyID(uuid.New()) }
func (id IdempotencyKeyID) String() string                   { return uuid.UUID(id).String() }
func (id IdempotencyKeyID) UUID() uuid.UUID                  { return uuid.UUID(id) }
func (id IdempotencyKeyID) IsZero() bool                     { return uuid.UUID(id) == uuid.Nil }
func IdempotencyKeyIDFromUUID(u uuid.UUID) IdempotencyKeyID  { return IdempotencyKeyID(u) }
func (id IdempotencyKeyID) Value() (driver.Value, error)     { return uuidValue(uuid.UUID(id)) }
func (id *IdempotencyKeyID) Scan(src any) error              { return uuidScan(src, (*uuid.UUID)(id)) }

// --- PolicyKeyID ---

func NewPolicyKeyID() PolicyKeyID                  { return PolicyKeyID(uuid.New()) }
func (id PolicyKeyID) String() string              { return uuid.UUID(id).String() }
func (id PolicyKeyID) UUID() uuid.UUID             { return uuid.UUID(id) }
func (id PolicyKeyID) IsZero() bool                { return uuid.UUID(id) == uuid.Nil }
func PolicyKeyIDFromUUID(u uuid.UUID) PolicyKeyID  { return PolicyKeyID(u) }
func (id PolicyKeyID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *PolicyKeyID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- NDRCaseID ---

func NewNDRCaseID() NDRCaseID                  { return NDRCaseID(uuid.New()) }
func (id NDRCaseID) String() string            { return uuid.UUID(id).String() }
func (id NDRCaseID) UUID() uuid.UUID           { return uuid.UUID(id) }
func (id NDRCaseID) IsZero() bool              { return uuid.UUID(id) == uuid.Nil }
func NDRCaseIDFromUUID(u uuid.UUID) NDRCaseID  { return NDRCaseID(u) }
func ParseNDRCaseID(s string) (NDRCaseID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return NDRCaseID{}, fmt.Errorf("core.ParseNDRCaseID: %w", err)
	}
	return NDRCaseID(u), nil
}
func (id NDRCaseID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *NDRCaseID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ReturnID ---

func NewReturnID() ReturnID                  { return ReturnID(uuid.New()) }
func (id ReturnID) String() string           { return uuid.UUID(id).String() }
func (id ReturnID) UUID() uuid.UUID          { return uuid.UUID(id) }
func (id ReturnID) IsZero() bool             { return uuid.UUID(id) == uuid.Nil }
func ReturnIDFromUUID(u uuid.UUID) ReturnID  { return ReturnID(u) }
func (id ReturnID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *ReturnID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- RTOID ---

func NewRTOID() RTOID                  { return RTOID(uuid.New()) }
func (id RTOID) String() string        { return uuid.UUID(id).String() }
func (id RTOID) UUID() uuid.UUID       { return uuid.UUID(id) }
func (id RTOID) IsZero() bool          { return uuid.UUID(id) == uuid.Nil }
func RTOIDFromUUID(u uuid.UUID) RTOID  { return RTOID(u) }
func (id RTOID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *RTOID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- CODRemittanceID ---

func NewCODRemittanceID() CODRemittanceID                  { return CODRemittanceID(uuid.New()) }
func (id CODRemittanceID) String() string                  { return uuid.UUID(id).String() }
func (id CODRemittanceID) UUID() uuid.UUID                 { return uuid.UUID(id) }
func (id CODRemittanceID) IsZero() bool                    { return uuid.UUID(id) == uuid.Nil }
func CODRemittanceIDFromUUID(u uuid.UUID) CODRemittanceID  { return CODRemittanceID(u) }
func (id CODRemittanceID) Value() (driver.Value, error)    { return uuidValue(uuid.UUID(id)) }
func (id *CODRemittanceID) Scan(src any) error             { return uuidScan(src, (*uuid.UUID)(id)) }

// --- WeightDisputeID ---

func NewWeightDisputeID() WeightDisputeID                  { return WeightDisputeID(uuid.New()) }
func (id WeightDisputeID) String() string                  { return uuid.UUID(id).String() }
func (id WeightDisputeID) UUID() uuid.UUID                 { return uuid.UUID(id) }
func (id WeightDisputeID) IsZero() bool                    { return uuid.UUID(id) == uuid.Nil }
func WeightDisputeIDFromUUID(u uuid.UUID) WeightDisputeID  { return WeightDisputeID(u) }
func (id WeightDisputeID) Value() (driver.Value, error)    { return uuidValue(uuid.UUID(id)) }
func (id *WeightDisputeID) Scan(src any) error             { return uuidScan(src, (*uuid.UUID)(id)) }

// --- NotificationID ---

func NewNotificationID() NotificationID                  { return NotificationID(uuid.New()) }
func (id NotificationID) String() string                 { return uuid.UUID(id).String() }
func (id NotificationID) UUID() uuid.UUID                { return uuid.UUID(id) }
func (id NotificationID) IsZero() bool                   { return uuid.UUID(id) == uuid.Nil }
func NotificationIDFromUUID(u uuid.UUID) NotificationID  { return NotificationID(u) }
func (id NotificationID) Value() (driver.Value, error)   { return uuidValue(uuid.UUID(id)) }
func (id *NotificationID) Scan(src any) error            { return uuidScan(src, (*uuid.UUID)(id)) }

// --- TicketID ---

func NewTicketID() TicketID                  { return TicketID(uuid.New()) }
func (id TicketID) String() string           { return uuid.UUID(id).String() }
func (id TicketID) UUID() uuid.UUID          { return uuid.UUID(id) }
func (id TicketID) IsZero() bool             { return uuid.UUID(id) == uuid.Nil }
func TicketIDFromUUID(u uuid.UUID) TicketID  { return TicketID(u) }
func (id TicketID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *TicketID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- OperatorActionID ---

func NewOperatorActionID() OperatorActionID                  { return OperatorActionID(uuid.New()) }
func (id OperatorActionID) String() string                   { return uuid.UUID(id).String() }
func (id OperatorActionID) UUID() uuid.UUID                  { return uuid.UUID(id) }
func (id OperatorActionID) IsZero() bool                     { return uuid.UUID(id) == uuid.Nil }
func OperatorActionIDFromUUID(u uuid.UUID) OperatorActionID  { return OperatorActionID(u) }
func (id OperatorActionID) Value() (driver.Value, error)     { return uuidValue(uuid.UUID(id)) }
func (id *OperatorActionID) Scan(src any) error              { return uuidScan(src, (*uuid.UUID)(id)) }

// --- RiskDetectionID ---

func NewRiskDetectionID() RiskDetectionID                  { return RiskDetectionID(uuid.New()) }
func (id RiskDetectionID) String() string                  { return uuid.UUID(id).String() }
func (id RiskDetectionID) UUID() uuid.UUID                 { return uuid.UUID(id) }
func (id RiskDetectionID) IsZero() bool                    { return uuid.UUID(id) == uuid.Nil }
func RiskDetectionIDFromUUID(u uuid.UUID) RiskDetectionID  { return RiskDetectionID(u) }
func (id RiskDetectionID) Value() (driver.Value, error)    { return uuidValue(uuid.UUID(id)) }
func (id *RiskDetectionID) Scan(src any) error             { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ContractID ---

func NewContractID() ContractID                  { return ContractID(uuid.New()) }
func (id ContractID) String() string             { return uuid.UUID(id).String() }
func (id ContractID) UUID() uuid.UUID            { return uuid.UUID(id) }
func (id ContractID) IsZero() bool               { return uuid.UUID(id) == uuid.Nil }
func ContractIDFromUUID(u uuid.UUID) ContractID  { return ContractID(u) }
func (id ContractID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *ContractID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- RateCardID ---

func NewRateCardID() RateCardID                  { return RateCardID(uuid.New()) }
func (id RateCardID) String() string             { return uuid.UUID(id).String() }
func (id RateCardID) UUID() uuid.UUID            { return uuid.UUID(id) }
func (id RateCardID) IsZero() bool               { return uuid.UUID(id) == uuid.Nil }
func RateCardIDFromUUID(u uuid.UUID) RateCardID  { return RateCardID(u) }
func ParseRateCardID(s string) (RateCardID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return RateCardID{}, fmt.Errorf("core.ParseRateCardID: %w", err)
	}
	return RateCardID(u), nil
}
func (id RateCardID) Value() (driver.Value, error) { return uuidValue(uuid.UUID(id)) }
func (id *RateCardID) Scan(src any) error          { return uuidScan(src, (*uuid.UUID)(id)) }

// --- ShipmentBookingID ---

func NewShipmentBookingID() ShipmentBookingID                  { return ShipmentBookingID(uuid.New()) }
func (id ShipmentBookingID) String() string                    { return uuid.UUID(id).String() }
func (id ShipmentBookingID) UUID() uuid.UUID                   { return uuid.UUID(id) }
func (id ShipmentBookingID) IsZero() bool                      { return uuid.UUID(id) == uuid.Nil }
func ShipmentBookingIDFromUUID(u uuid.UUID) ShipmentBookingID  { return ShipmentBookingID(u) }
func (id ShipmentBookingID) Value() (driver.Value, error)      { return uuidValue(uuid.UUID(id)) }
func (id *ShipmentBookingID) Scan(src any) error               { return uuidScan(src, (*uuid.UUID)(id)) }

// --- TrackingEventID ---

func NewTrackingEventID() TrackingEventID                  { return TrackingEventID(uuid.New()) }
func (id TrackingEventID) String() string                  { return uuid.UUID(id).String() }
func (id TrackingEventID) UUID() uuid.UUID                 { return uuid.UUID(id) }
func (id TrackingEventID) IsZero() bool                    { return uuid.UUID(id) == uuid.Nil }
func TrackingEventIDFromUUID(u uuid.UUID) TrackingEventID  { return TrackingEventID(u) }
func (id TrackingEventID) Value() (driver.Value, error)    { return uuidValue(uuid.UUID(id)) }
func (id *TrackingEventID) Scan(src any) error             { return uuidScan(src, (*uuid.UUID)(id)) }

// --- shared SQL helpers ---

func uuidValue(u uuid.UUID) (driver.Value, error) {
	if u == uuid.Nil {
		return nil, nil
	}
	return u.String(), nil
}

func uuidScan(src any, dst *uuid.UUID) error {
	var u uuid.UUID
	if err := u.Scan(src); err != nil {
		return fmt.Errorf("core: scan uuid: %w", err)
	}
	*dst = u
	return nil
}
