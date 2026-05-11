package core

// JSON marshal/unmarshal for every typed ID. They serialize as the standard
// UUID string ("xxxxxxxx-xxxx-...") rather than Go's default byte array.
//
// Defined together so adding a new typed ID is a one-line change here.

import (
	"github.com/google/uuid"
)

func (id SellerID) MarshalJSON() ([]byte, error)             { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *SellerID) UnmarshalJSON(b []byte) error            { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id SubSellerID) MarshalJSON() ([]byte, error)          { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *SubSellerID) UnmarshalJSON(b []byte) error         { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id UserID) MarshalJSON() ([]byte, error)               { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *UserID) UnmarshalJSON(b []byte) error              { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id OrderID) MarshalJSON() ([]byte, error)              { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *OrderID) UnmarshalJSON(b []byte) error             { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ShipmentID) MarshalJSON() ([]byte, error)           { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ShipmentID) UnmarshalJSON(b []byte) error          { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id CarrierID) MarshalJSON() ([]byte, error)            { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *CarrierID) UnmarshalJSON(b []byte) error           { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ChannelID) MarshalJSON() ([]byte, error)            { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ChannelID) UnmarshalJSON(b []byte) error           { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id PickupLocationID) MarshalJSON() ([]byte, error)     { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *PickupLocationID) UnmarshalJSON(b []byte) error    { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ProductID) MarshalJSON() ([]byte, error)            { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ProductID) UnmarshalJSON(b []byte) error           { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id BuyerID) MarshalJSON() ([]byte, error)              { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *BuyerID) UnmarshalJSON(b []byte) error             { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id BuyerAddressID) MarshalJSON() ([]byte, error)       { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *BuyerAddressID) UnmarshalJSON(b []byte) error      { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id AuditEventID) MarshalJSON() ([]byte, error)         { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *AuditEventID) UnmarshalJSON(b []byte) error        { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id HoldID) MarshalJSON() ([]byte, error)               { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *HoldID) UnmarshalJSON(b []byte) error              { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id LedgerEntryID) MarshalJSON() ([]byte, error)        { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *LedgerEntryID) UnmarshalJSON(b []byte) error       { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id AllocationDecisionID) MarshalJSON() ([]byte, error) { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *AllocationDecisionID) UnmarshalJSON(b []byte) error {
	return uuidUnmarshalJSON(b, (*uuid.UUID)(id))
}
func (id SessionID) MarshalJSON() ([]byte, error)         { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *SessionID) UnmarshalJSON(b []byte) error        { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id OutboxEventID) MarshalJSON() ([]byte, error)     { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *OutboxEventID) UnmarshalJSON(b []byte) error    { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id IdempotencyKeyID) MarshalJSON() ([]byte, error)  { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *IdempotencyKeyID) UnmarshalJSON(b []byte) error { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id PolicyKeyID) MarshalJSON() ([]byte, error)       { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *PolicyKeyID) UnmarshalJSON(b []byte) error      { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id NDRCaseID) MarshalJSON() ([]byte, error)         { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *NDRCaseID) UnmarshalJSON(b []byte) error        { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ReturnID) MarshalJSON() ([]byte, error)          { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ReturnID) UnmarshalJSON(b []byte) error         { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id RTOID) MarshalJSON() ([]byte, error)             { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *RTOID) UnmarshalJSON(b []byte) error            { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id CODRemittanceID) MarshalJSON() ([]byte, error)   { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *CODRemittanceID) UnmarshalJSON(b []byte) error  { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id WeightDisputeID) MarshalJSON() ([]byte, error)   { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *WeightDisputeID) UnmarshalJSON(b []byte) error  { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id NotificationID) MarshalJSON() ([]byte, error)    { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *NotificationID) UnmarshalJSON(b []byte) error   { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id TicketID) MarshalJSON() ([]byte, error)          { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *TicketID) UnmarshalJSON(b []byte) error         { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id OperatorActionID) MarshalJSON() ([]byte, error)  { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *OperatorActionID) UnmarshalJSON(b []byte) error { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id RiskDetectionID) MarshalJSON() ([]byte, error)   { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *RiskDetectionID) UnmarshalJSON(b []byte) error  { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ContractID) MarshalJSON() ([]byte, error)        { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ContractID) UnmarshalJSON(b []byte) error       { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id RateCardID) MarshalJSON() ([]byte, error)        { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *RateCardID) UnmarshalJSON(b []byte) error       { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }
func (id ShipmentBookingID) MarshalJSON() ([]byte, error) { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *ShipmentBookingID) UnmarshalJSON(b []byte) error {
	return uuidUnmarshalJSON(b, (*uuid.UUID)(id))
}
func (id TrackingEventID) MarshalJSON() ([]byte, error)    { return uuidMarshalJSON(uuid.UUID(id)) }
func (id *TrackingEventID) UnmarshalJSON(b []byte) error   { return uuidUnmarshalJSON(b, (*uuid.UUID)(id)) }

func uuidMarshalJSON(u uuid.UUID) ([]byte, error) {
	if u == uuid.Nil {
		return []byte(`null`), nil
	}
	// 36 chars + 2 quotes
	out := make([]byte, 0, 38)
	out = append(out, '"')
	out = append(out, u.String()...)
	out = append(out, '"')
	return out, nil
}

func uuidUnmarshalJSON(b []byte, dst *uuid.UUID) error {
	if len(b) == 4 && string(b) == "null" {
		*dst = uuid.Nil
		return nil
	}
	if len(b) < 2 {
		*dst = uuid.Nil
		return nil
	}
	s := string(b[1 : len(b)-1])
	parsed, err := uuid.Parse(s)
	if err != nil {
		return err
	}
	*dst = parsed
	return nil
}
