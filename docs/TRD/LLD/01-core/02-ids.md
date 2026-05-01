# Core: Typed IDs (`internal/core/ids.go`)

> Type-safe identifiers. Prevents passing a `SellerID` where an `OrderID` is expected.

## Purpose

Every entity (Seller, Order, Shipment, etc.) has its own typed ID. Compile-time prevents mix-ups. Database type is `UUID`.

## Dependencies

- `github.com/google/uuid`

## Public API

```go
package core

import (
    "database/sql/driver"
    "errors"
    "fmt"

    "github.com/google/uuid"
)

// SellerID identifies a seller (the customer of Pikshipp).
type SellerID uuid.UUID

// SubSellerID identifies a sub-seller (branch/subsidiary of a seller).
type SubSellerID uuid.UUID

// UserID identifies a user (a human or service account within a seller).
type UserID uuid.UUID

// OrderID identifies a canonical order.
type OrderID uuid.UUID

// ShipmentID identifies a shipment (a parcel; one AWB).
type ShipmentID uuid.UUID

// CarrierID identifies a carrier.
type CarrierID uuid.UUID

// ChannelID identifies a per-seller channel connection.
type ChannelID uuid.UUID

// PickupLocationID identifies a pickup location.
type PickupLocationID uuid.UUID

// ProductID identifies a SKU master record.
type ProductID uuid.UUID

// BuyerID identifies a buyer (per-seller; not a global identity).
type BuyerID uuid.UUID

// AuditEventID identifies an audit event.
type AuditEventID uuid.UUID

// HoldID is a wallet two-phase reservation handle.
type HoldID uuid.UUID

// LedgerEntryID identifies a wallet ledger entry.
type LedgerEntryID uuid.UUID

// AllocationDecisionID identifies an allocation decision record.
type AllocationDecisionID uuid.UUID

// SessionID identifies a server-side session.
type SessionID uuid.UUID

// And so on for every domain entity. All follow the same pattern.

// ---------- Constructor ----------

// NewSellerID generates a new random SellerID.
func NewSellerID() SellerID {
    return SellerID(uuid.New())
}

// (Similar New*ID functions for each type.)

// ---------- String / parse ----------

// String returns the canonical UUID string form.
func (id SellerID) String() string {
    return uuid.UUID(id).String()
}

// IsZero reports whether id is the zero value.
func (id SellerID) IsZero() bool {
    return uuid.UUID(id) == uuid.Nil
}

// ParseSellerID parses a UUID string into a SellerID.
func ParseSellerID(s string) (SellerID, error) {
    u, err := uuid.Parse(s)
    if err != nil {
        return SellerID{}, fmt.Errorf("core.ParseSellerID: %w", err)
    }
    return SellerID(u), nil
}

// ---------- SQL Driver ----------

// Value implements driver.Valuer for database serialization.
func (id SellerID) Value() (driver.Value, error) {
    if uuid.UUID(id) == uuid.Nil {
        return nil, nil
    }
    return uuid.UUID(id).String(), nil
}

// Scan implements sql.Scanner for database deserialization.
func (id *SellerID) Scan(src any) error {
    var u uuid.UUID
    err := u.Scan(src)
    if err != nil {
        return fmt.Errorf("core.SellerID.Scan: %w", err)
    }
    *id = SellerID(u)
    return nil
}

// ---------- JSON ----------

// MarshalJSON serializes as a JSON string of the UUID.
func (id SellerID) MarshalJSON() ([]byte, error) {
    return uuid.UUID(id).MarshalJSON()
}

// UnmarshalJSON deserializes from a JSON string.
func (id *SellerID) UnmarshalJSON(data []byte) error {
    var u uuid.UUID
    err := u.UnmarshalJSON(data)
    if err != nil {
        return fmt.Errorf("core.SellerID.UnmarshalJSON: %w", err)
    }
    *id = SellerID(u)
    return nil
}
```

The pattern is mechanical. We could code-gen all of these from a list, but for v0 we hand-write them — it's <10 lines per type and one-time.

## Implementation notes

### Why typed IDs and not raw UUID

A function signature like:

```go
func GetOrder(ctx context.Context, sellerID SellerID, orderID OrderID) (*Order, error)
```

prevents `GetOrder(ctx, orderID, sellerID)` (wrong order) at compile time. With raw UUIDs, this is a runtime bug.

### Why not int64 / string IDs

- UUID-shaped IDs make data joinable across environments without sequence collisions.
- UUIDs in URLs are non-enumerable (vs. sequential integers).
- pgcrypto's `gen_random_uuid()` is fast and uniformly distributed.

### Why Postgres UUID, not BIGSERIAL

- See above; non-enumerability and merge-ability.
- Index size is larger but acceptable.

### Underlying type choice

We chose `type SellerID uuid.UUID` (named type), not a struct. This keeps it value-type, comparable, and small. Methods are added via receiver functions.

The downside: when sqlc reads a row, the generated code uses `uuid.UUID`; we then explicitly cast `core.SellerID(generatedUUID)`. Acceptable.

### Zero value handling

`SellerID{}` is the zero value (all-zero UUID, equivalent to `uuid.Nil`). Used to represent "unset". Functions that take SellerID as a parameter should reject the zero value when set is required:

```go
func (s *serviceImpl) DoThing(ctx context.Context, sellerID core.SellerID, ...) error {
    if sellerID.IsZero() {
        return ErrInvalidArgument
    }
    // ...
}
```

## Display IDs

For human-readable IDs (e.g., `PSO-lotus-100123`), we **don't** use the UUID. Instead, each entity has a `display_id` column with a human-readable string for UI use. The UUID remains the database PK.

Display ID generation lives in the domain module (e.g., `orders.GenerateDisplayID`).

## Database setup

Every PK is `UUID NOT NULL DEFAULT gen_random_uuid()`. Indexes are on the UUID; sufficient.

## Testing

```go
func TestSellerID_RoundTrip(t *testing.T) {
    id := NewSellerID()
    s := id.String()
    parsed, err := ParseSellerID(s)
    if err != nil { t.Fatal(err) }
    if parsed != id {
        t.Errorf("round-trip failed: %v != %v", id, parsed)
    }
}

func TestSellerID_JSON(t *testing.T) {
    id := NewSellerID()
    data, err := json.Marshal(id)
    if err != nil { t.Fatal(err) }

    var parsed SellerID
    err = json.Unmarshal(data, &parsed)
    if err != nil { t.Fatal(err) }
    if parsed != id { t.Errorf("JSON round-trip failed") }
}

func TestSellerID_SQL(t *testing.T) {
    id := NewSellerID()

    val, err := id.Value()
    if err != nil { t.Fatal(err) }

    var parsed SellerID
    err = parsed.Scan(val)
    if err != nil { t.Fatal(err) }
    if parsed != id { t.Errorf("SQL round-trip failed") }
}

func TestSellerID_IsZero(t *testing.T) {
    var zero SellerID
    if !zero.IsZero() { t.Error("expected IsZero true") }

    nonZero := NewSellerID()
    if nonZero.IsZero() { t.Error("expected IsZero false") }
}
```

## Performance

- Construction: zero-allocation (UUID gen is non-allocating).
- String: 1 allocation.
- Comparison: integer compare (UUIDs are 16 bytes; Go compares as bytes).

## Open questions

- Code-gen for ID types? Considered. Pass for v0; ~30 types × 6 methods = 180 functions, written once. If we ever add 30 more types, generate.
