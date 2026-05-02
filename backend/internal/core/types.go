package core

// Pincode is a 6-digit Indian PIN code, stored as string to preserve any
// leading zeros and to make code reviews easier than int parsing.
type Pincode string

// IsValid reports whether p is exactly 6 ASCII digits.
func (p Pincode) IsValid() bool {
	if len(p) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		c := p[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// String returns the pincode as-is.
func (p Pincode) String() string { return string(p) }

// Address is a postal address. Per LLD §01-core/05-types: kept minimal here;
// each domain that needs richer address modeling (geocoding, validation)
// extends locally.
type Address struct {
	Line1   string  `json:"line1"`
	Line2   string  `json:"line2,omitempty"`
	City    string  `json:"city"`
	State   string  `json:"state"`
	Country string  `json:"country"` // ISO 3166-1 alpha-2; "IN" by default
	Pincode Pincode `json:"pincode"`
}

// ContactInfo carries phone + email; both optional but at least one
// must be present at the call sites that require it (validated locally).
type ContactInfo struct {
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"` // E.164, e.g. +919812345678
	Email string `json:"email,omitempty"`
}

// PaymentMode is how the buyer pays for the order.
type PaymentMode string

const (
	PaymentModePrepaid PaymentMode = "prepaid"
	PaymentModeCOD     PaymentMode = "cod"
)

// SellerType classifies a seller for policy defaults.
type SellerType string

const (
	SellerTypeSmallMedium SellerType = "small_business" // matches DB check constraint
	SellerTypeMidMarket   SellerType = "mid_market"
	SellerTypeEnterprise  SellerType = "enterprise"
)

// StringSet is an immutable set of strings backed by a map.
type StringSet map[string]struct{}

// NewStringSet constructs a StringSet from the given values.
func NewStringSet(vals ...string) StringSet {
	m := make(StringSet, len(vals))
	for _, v := range vals {
		m[v] = struct{}{}
	}
	return m
}

// Has reports whether s is in the set.
func (ss StringSet) Has(s string) bool {
	_, ok := ss[s]
	return ok
}

// Slice returns the set as a sorted slice.
func (ss StringSet) Slice() []string {
	out := make([]string, 0, len(ss))
	for k := range ss {
		out = append(out, k)
	}
	return out
}

// IsValid reports whether m is a known PaymentMode.
func (m PaymentMode) IsValid() bool {
	switch m {
	case PaymentModePrepaid, PaymentModeCOD:
		return true
	}
	return false
}

// ServiceType is the carrier service level (standard, express, etc.).
type ServiceType string

const (
	ServiceTypeStandard ServiceType = "standard"
	ServiceTypeExpress  ServiceType = "express"
	ServiceTypeSameDay  ServiceType = "same_day"
	ServiceTypeLite     ServiceType = "lite"
)

// SellerRole is a named capability granted to a user within a seller.
// Roles are stored as a JSON array in seller_user.roles_jsonb.
type SellerRole string

const (
	RoleOwner   SellerRole = "owner"
	RoleManager SellerRole = "manager"
	RoleOps     SellerRole = "ops"
	RoleFinance SellerRole = "finance"
	RoleSupport SellerRole = "support"
	RoleViewer  SellerRole = "viewer"
)

// HasAnyRole reports whether roles contains at least one of the required roles.
func HasAnyRole(roles []SellerRole, required []SellerRole) bool {
	for _, r := range roles {
		for _, req := range required {
			if r == req {
				return true
			}
		}
	}
	return false
}
