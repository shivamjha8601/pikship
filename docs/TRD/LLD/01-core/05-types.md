# Core: Domain types (`internal/core/*.go`)

> Pure data types referenced across modules: Pincode, Address, CanonicalStatus, etc.

## Files

```
internal/core/
├── doc.go
├── money.go              ← Paise (separate LLD)
├── ids.go                ← typed IDs (separate LLD)
├── clock.go              ← Clock interface (separate LLD)
├── errors.go             ← sentinel errors (separate LLD)
├── pincode.go            ← Pincode validation
├── address.go            ← Address struct
├── canonical_status.go   ← Shipment / Order canonical states + transitions
├── ndr_reason.go         ← canonical NDR reasons
├── carrier_service.go    ← service types enum
├── payment_mode.go       ← prepaid / cod
├── seller_role.go        ← role enum
├── seller_type.go        ← seller-type enum (small_smb / mid_market / enterprise)
├── e164.go               ← phone normalization
└── string_set.go         ← small set utility
```

## `pincode.go`

```go
package core

import (
    "errors"
    "fmt"
    "regexp"
)

// Pincode is a validated Indian postal code (6 digits).
//
// Storage: TEXT(6) in DB. Validation: regex.
type Pincode string

var pincodeRe = regexp.MustCompile(`^[1-9][0-9]{5}$`)

// ErrInvalidPincode is returned by ParsePincode for malformed inputs.
var ErrInvalidPincode = errors.New("core: invalid pincode")

// ParsePincode validates and returns a Pincode. Returns ErrInvalidPincode
// for inputs that don't match the Indian postal code format.
func ParsePincode(s string) (Pincode, error) {
    if !pincodeRe.MatchString(s) {
        return "", fmt.Errorf("core.ParsePincode: %q: %w", s, ErrInvalidPincode)
    }
    return Pincode(s), nil
}

// String returns the pincode as a 6-digit string.
func (p Pincode) String() string {
    return string(p)
}

// IsZero reports whether p is unset.
func (p Pincode) IsZero() bool {
    return p == ""
}

// FirstDigit returns the leading digit (region indicator).
// 1 = Delhi/HR/HP/J&K/Punjab/Chandigarh
// 2 = UP/Uttarakhand
// 3 = Rajasthan/Gujarat/Daman
// 4 = Maharashtra/Goa/MP/Chhattisgarh
// 5 = AP/Telangana/Karnataka
// 6 = TN/Kerala/Pondicherry/Lakshadweep
// 7 = WB/Odisha/Bihar/Jharkhand/NE states/A&N
// 8 = Bihar/Jharkhand
func (p Pincode) FirstDigit() int {
    if len(p) == 0 {
        return 0
    }
    return int(p[0]) - '0'
}
```

Tests:
```go
func TestParsePincode(t *testing.T) {
    cases := []struct{
        in string; ok bool
    }{
        {"110001", true},
        {"400070", true},
        {"00000A", false},
        {"100000", false},  // starts with 0... wait, our regex says [1-9]. Some pincodes do start with 0? No — Indian pincodes are 100000–999999.
        {"1234", false},
        {"1234567", false},
        {"", false},
    }
    for _, c := range cases {
        _, err := ParsePincode(c.in)
        gotOK := err == nil
        if gotOK != c.ok {
            t.Errorf("ParsePincode(%q) ok=%v, want %v", c.in, gotOK, c.ok)
        }
    }
}
```

## `address.go`

```go
package core

import "errors"

// Address is a postal address. All fields are required except where noted.
type Address struct {
    Line1         string  `json:"line1"`
    Line2         string  `json:"line2,omitempty"`
    Landmark      string  `json:"landmark,omitempty"`
    City          string  `json:"city"`
    State         StateCode `json:"state"`        // ISO 3166-2:IN code
    Pincode       Pincode `json:"pincode"`
    Country       string  `json:"country"`        // always "IN" at v0
    ContactName   string  `json:"contact_name"`
    ContactPhone  E164    `json:"contact_phone"`
    AlternatePhone *E164  `json:"alternate_phone,omitempty"`
    LocationType  LocationType `json:"location_type"`
    Geo           *GeoCoord `json:"geo,omitempty"`
}

// StateCode is an ISO 3166-2:IN code (e.g., "MH", "KA", "DL").
type StateCode string

// LocationType classifies a delivery / pickup destination.
type LocationType string

const (
    LocationTypeHome        LocationType = "home"
    LocationTypeOffice      LocationType = "office"
    LocationTypeShop        LocationType = "shop"
    LocationTypeWarehouse   LocationType = "warehouse"
    LocationTypePickupPoint LocationType = "pickup_point"
)

// GeoCoord is a latitude/longitude pair.
type GeoCoord struct {
    Latitude  float64 `json:"lat"`
    Longitude float64 `json:"lng"`
}

// ErrInvalidAddress is the categorical sentinel; specific issues wrap it.
var ErrInvalidAddress = errors.New("core: invalid address")

// Validate checks all fields. Returns nil if valid; otherwise wraps
// ErrInvalidAddress with a description.
func (a Address) Validate() error {
    if len(a.Line1) < 3 {
        return errAddrf("line1 too short: %q", a.Line1)
    }
    if a.City == "" {
        return errAddrf("city missing")
    }
    if a.State == "" {
        return errAddrf("state missing")
    }
    if a.Pincode == "" {
        return errAddrf("pincode missing")
    }
    if a.Country != "IN" {
        return errAddrf("country must be IN, got %q", a.Country)
    }
    if a.ContactName == "" {
        return errAddrf("contact_name missing")
    }
    if a.ContactPhone == "" {
        return errAddrf("contact_phone missing")
    }
    return nil
}

func errAddrf(format string, args ...any) error {
    return fmt.Errorf("core.Address: "+format+": %w", append(args, ErrInvalidAddress)...)
}

// MaskedDisplay returns the address with PII partially masked.
// Used for buyer-facing tracking pages (we show the address but mask sensitive parts).
func (a Address) MaskedDisplay() Address {
    masked := a
    if len(a.Line1) > 6 {
        masked.Line1 = a.Line1[:3] + "***" + a.Line1[len(a.Line1)-3:]
    } else {
        masked.Line1 = "****"
    }
    masked.Line2 = ""
    masked.ContactPhone = E164(maskPhone(string(a.ContactPhone)))
    return masked
}

func maskPhone(p string) string {
    if len(p) < 4 {
        return "****"
    }
    return "****" + p[len(p)-4:]
}
```

## `canonical_status.go`

```go
package core

import "fmt"

// CanonicalStatus is the Pikshipp-internal shipment status, normalized
// across all carriers. See HLD 03-services/05-tracking-and-status.md for
// the per-carrier mapping discipline.
type CanonicalStatus string

const (
    StatusCreated         CanonicalStatus = "created"
    StatusBooked          CanonicalStatus = "booked"
    StatusPickupPending   CanonicalStatus = "pickup_pending"
    StatusPickedUp        CanonicalStatus = "picked_up"
    StatusInTransit       CanonicalStatus = "in_transit"
    StatusOutForDelivery  CanonicalStatus = "out_for_delivery"
    StatusDelivered       CanonicalStatus = "delivered"
    StatusNDR             CanonicalStatus = "ndr"
    StatusRTOInitiated    CanonicalStatus = "rto_initiated"
    StatusRTOInTransit    CanonicalStatus = "rto_in_transit"
    StatusRTODelivered    CanonicalStatus = "rto_delivered"
    StatusCancelled       CanonicalStatus = "cancelled"
    StatusLost            CanonicalStatus = "lost"
    StatusDamaged         CanonicalStatus = "damaged"
    StatusUnknown         CanonicalStatus = "unknown"
)

// IsTerminal reports whether further transitions are expected.
func (s CanonicalStatus) IsTerminal() bool {
    switch s {
    case StatusDelivered, StatusRTODelivered, StatusCancelled, StatusLost, StatusDamaged:
        return true
    }
    return false
}

// allowedTransitions defines the legal state machine.
// Maps from-status → set of allowed to-statuses.
var allowedTransitions = map[CanonicalStatus]map[CanonicalStatus]bool{
    StatusBooked: {
        StatusPickupPending: true,
        StatusCancelled:     true,
    },
    StatusPickupPending: {
        StatusPickedUp: true,
        StatusCancelled: true,
    },
    StatusPickedUp: {
        StatusInTransit: true,
    },
    StatusInTransit: {
        StatusOutForDelivery: true,
        StatusLost:           true,
        StatusDamaged:        true,
    },
    StatusOutForDelivery: {
        StatusDelivered: true,
        StatusNDR:       true,
        StatusLost:      true,
        StatusDamaged:   true,
    },
    StatusNDR: {
        StatusOutForDelivery: true,  // reattempt
        StatusRTOInitiated:   true,
    },
    StatusRTOInitiated: {
        StatusRTOInTransit: true,
    },
    StatusRTOInTransit: {
        StatusRTODelivered: true,
    },
    // terminal states have no allowed transitions
}

// CanTransition reports whether moving from `from` to `to` is allowed
// by the canonical state machine.
//
// Special case: any state -> StatusUnknown is allowed for ingestion of
// unmappable carrier codes (we persist the event but don't transition).
func CanTransition(from, to CanonicalStatus) bool {
    if to == StatusUnknown {
        return true  // pseudo-allowed; caller decides what to do
    }
    if from == to {
        return true  // no-op
    }
    allowed, ok := allowedTransitions[from]
    return ok && allowed[to]
}

// ValidateTransition returns an error if the transition is illegal.
func ValidateTransition(from, to CanonicalStatus) error {
    if CanTransition(from, to) {
        return nil
    }
    return fmt.Errorf("invalid transition %s → %s", from, to)
}

// IsValid reports whether s is a known canonical status.
func (s CanonicalStatus) IsValid() bool {
    switch s {
    case StatusCreated, StatusBooked, StatusPickupPending, StatusPickedUp,
        StatusInTransit, StatusOutForDelivery, StatusDelivered, StatusNDR,
        StatusRTOInitiated, StatusRTOInTransit, StatusRTODelivered,
        StatusCancelled, StatusLost, StatusDamaged, StatusUnknown:
        return true
    }
    return false
}
```

Tests:
```go
func TestCanTransition(t *testing.T) {
    cases := []struct {
        from, to CanonicalStatus
        want     bool
    }{
        {StatusBooked, StatusPickupPending, true},
        {StatusBooked, StatusDelivered, false},          // skip steps; not allowed
        {StatusDelivered, StatusInTransit, false},       // regression; not allowed
        {StatusNDR, StatusOutForDelivery, true},         // reattempt
        {StatusInTransit, StatusUnknown, true},          // pseudo-allowed
        {StatusInTransit, StatusInTransit, true},        // no-op
    }
    for _, c := range cases {
        got := CanTransition(c.from, c.to)
        if got != c.want {
            t.Errorf("CanTransition(%s, %s) = %v; want %v", c.from, c.to, got, c.want)
        }
    }
}
```

## `ndr_reason.go`

```go
package core

// NDRReason is the canonical reason for a delivery failure.
type NDRReason string

const (
    NDRReasonBuyerUnavailable NDRReason = "buyer_unavailable"
    NDRReasonWrongAddress     NDRReason = "wrong_address"
    NDRReasonRefused          NDRReason = "refused"
    NDRReasonPremisesLocked   NDRReason = "premises_locked"
    NDRReasonCODNotReady      NDRReason = "cod_not_ready"
    NDRReasonOther            NDRReason = "other"
    NDRReasonUnknown          NDRReason = ""  // unknown/unmapped
)

// IsValid reports whether r is a known reason.
func (r NDRReason) IsValid() bool {
    switch r {
    case NDRReasonBuyerUnavailable, NDRReasonWrongAddress, NDRReasonRefused,
        NDRReasonPremisesLocked, NDRReasonCODNotReady, NDRReasonOther, NDRReasonUnknown:
        return true
    }
    return false
}
```

## `carrier_service.go`

```go
package core

// ServiceType is the kind of shipping service offered by a carrier.
type ServiceType string

const (
    ServiceSurface     ServiceType = "surface"
    ServiceAir         ServiceType = "air"
    ServiceExpress     ServiceType = "express"
    ServiceHyperlocal  ServiceType = "hyperlocal"
    ServiceB2BLTL      ServiceType = "b2b_ltl"
    ServiceB2BFTL      ServiceType = "b2b_ftl"
)

func (s ServiceType) IsValid() bool {
    switch s {
    case ServiceSurface, ServiceAir, ServiceExpress,
        ServiceHyperlocal, ServiceB2BLTL, ServiceB2BFTL:
        return true
    }
    return false
}
```

## `payment_mode.go`

```go
package core

type PaymentMode string

const (
    PaymentModePrepaid PaymentMode = "prepaid"
    PaymentModeCOD     PaymentMode = "cod"
)

func (m PaymentMode) IsValid() bool {
    return m == PaymentModePrepaid || m == PaymentModeCOD
}
```

## `seller_role.go`

```go
package core

// SellerRole is a role within a seller organization.
type SellerRole string

const (
    RoleOwner    SellerRole = "owner"
    RoleManager  SellerRole = "manager"
    RoleOperator SellerRole = "operator"
    RoleFinance  SellerRole = "finance"
    RoleReadOnly SellerRole = "read_only"
)

// AllSellerRoles in priority order (Owner > Manager > ...).
var AllSellerRoles = []SellerRole{RoleOwner, RoleManager, RoleOperator, RoleFinance, RoleReadOnly}

// HasAny reports whether at least one of `have` matches one of `want`.
func HasAnyRole(have []SellerRole, want []SellerRole) bool {
    for _, h := range have {
        for _, w := range want {
            if h == w {
                return true
            }
        }
    }
    return false
}
```

## `seller_type.go`

```go
package core

// SellerType is the bundled-defaults type for a seller.
//
// The policy engine resolves per-key defaults using this type.
type SellerType string

const (
    SellerTypeSmallSMB     SellerType = "small_smb"
    SellerTypeMidMarket    SellerType = "mid_market"
    SellerTypeEnterprise   SellerType = "enterprise"
    SellerTypeCustom       SellerType = "custom"
)

func (t SellerType) IsValid() bool {
    switch t {
    case SellerTypeSmallSMB, SellerTypeMidMarket, SellerTypeEnterprise, SellerTypeCustom:
        return true
    }
    return false
}
```

## `e164.go`

```go
package core

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

// E164 is an ITU-T E.164 formatted phone number with leading "+".
//
// At v0 we constrain to Indian numbers (+91, 10 digits, leading 6-9).
// Wider validation is added when we ship outside India.
type E164 string

var indianMobileRe = regexp.MustCompile(`^\+91[6-9][0-9]{9}$`)

var ErrInvalidPhone = errors.New("core: invalid phone")

// ParseE164 validates and normalizes a phone string.
// Accepts inputs like:
//   "+919876543210"
//   "919876543210"
//   "9876543210"
// Strips spaces and hyphens. Rejects anything not matching Indian mobile format.
func ParseE164(s string) (E164, error) {
    s = strings.NewReplacer(" ", "", "-", "").Replace(s)
    if !strings.HasPrefix(s, "+") {
        if strings.HasPrefix(s, "91") && len(s) == 12 {
            s = "+" + s
        } else if len(s) == 10 {
            s = "+91" + s
        } else {
            return "", fmt.Errorf("core.ParseE164: %q: %w", s, ErrInvalidPhone)
        }
    }
    if !indianMobileRe.MatchString(s) {
        return "", fmt.Errorf("core.ParseE164: %q: %w", s, ErrInvalidPhone)
    }
    return E164(s), nil
}

// String returns the E.164 form including leading "+".
func (e E164) String() string { return string(e) }

// IsZero reports whether e is unset.
func (e E164) IsZero() bool { return e == "" }

// LastN returns the last n characters of the phone (for masked display).
func (e E164) LastN(n int) string {
    s := string(e)
    if n <= 0 || n >= len(s) {
        return s
    }
    return s[len(s)-n:]
}
```

## `string_set.go`

```go
package core

// StringSet is a small set of strings backed by a map.
//
// Use for sets where membership tests dominate (e.g., allowed_carriers).
// For larger or hot sets, consider a more compact representation.
type StringSet map[string]struct{}

func NewStringSet(items ...string) StringSet {
    s := make(StringSet, len(items))
    for _, it := range items {
        s[it] = struct{}{}
    }
    return s
}

func (s StringSet) Has(item string) bool {
    _, ok := s[item]
    return ok
}

func (s StringSet) Add(item string) { s[item] = struct{}{} }

func (s StringSet) Remove(item string) { delete(s, item) }

func (s StringSet) Len() int { return len(s) }

func (s StringSet) Slice() []string {
    out := make([]string, 0, len(s))
    for k := range s {
        out = append(out, k)
    }
    return out
}
```

## Performance notes

These types are small, mostly value types, no allocation in hot paths. `string` and `int64`-based aliases are cheap. `regexp` validators are compiled once at package init.

## Open questions

- Should `Address.Validate()` enforce pincode-state consistency (e.g., MH pincode shouldn't claim Karnataka)? Currently no; we surface as a warning, not blocker. Decide at LLD review.
- `StateCode` enum vs string: leave as `type StateCode string` with a validator function rather than 36 constants (clutter). LLD validator returns error for unknown.
- `E164` for non-Indian numbers (international expansion): regex needs to relax. Defer.
