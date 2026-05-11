package policy

import "github.com/vishal1132/pikshipp/backend/internal/core"

// Type is the JSON value type expected for a setting.
type Type string

const (
	TypeInt64      Type = "int64"
	TypePaise      Type = "paise"
	TypeString     Type = "string"
	TypeBool       Type = "bool"
	TypeDuration   Type = "duration"
	TypeStringList Type = "string_list"
	TypeStringSet  Type = "string_set"
)

// Definition declares one policy setting key.
type Definition struct {
	Key             Key
	ValueType       Type
	DefaultGlobal   Value
	DefaultsByType  map[core.SellerType]Value
	LockCapable     bool
	OverrideAllowed bool
	AuditOnRead     bool
	Description     string
	RegisteredIn    string
	AddedInVersion  string
}

// Definitions is the canonical, Go-coded registry of every valid key.
// It is sealed at startup; seed.go upserts rows to policy_setting_definition.
var Definitions = []Definition{
	// Wallet
	{
		Key: KeyWalletPosture, ValueType: TypeString,
		DefaultGlobal: StringValue("prepaid_only"),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  StringValue("hybrid"),
			core.SellerTypeEnterprise: StringValue("credit_only"),
		},
		LockCapable: true, OverrideAllowed: true,
		Description: "Wallet payment posture; affects credit limit availability",
		RegisteredIn: "wallet", AddedInVersion: "v0",
	},
	{
		Key: KeyWalletCreditLimitInr, ValueType: TypePaise,
		DefaultGlobal: PaiseValue(0),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  PaiseValue(core.FromRupees(100_000)),
			core.SellerTypeEnterprise: PaiseValue(core.FromRupees(500_000)),
		},
		LockCapable: true, OverrideAllowed: true, AuditOnRead: true,
		Description: "Credit limit in paise",
		RegisteredIn: "wallet", AddedInVersion: "v0",
	},
	{
		Key: KeyWalletGraceNegativeAmount, ValueType: TypePaise,
		DefaultGlobal: PaiseValue(core.FromRupees(500)),
		LockCapable: true, OverrideAllowed: true,
		Description: "Wallet may go negative this much before suspension",
		RegisteredIn: "wallet", AddedInVersion: "v0",
	},

	// COD
	{
		Key: KeyCODEnabled, ValueType: TypeBool,
		DefaultGlobal: BoolValue(false),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  BoolValue(true),
			core.SellerTypeEnterprise: BoolValue(true),
		},
		LockCapable: true, OverrideAllowed: true,
		Description: "Whether COD is allowed for this seller",
		RegisteredIn: "cod", AddedInVersion: "v0",
	},
	{
		Key: KeyCODRemittanceCycleDays, ValueType: TypeInt64,
		DefaultGlobal: Int64Value(5),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  Int64Value(3),
			core.SellerTypeEnterprise: Int64Value(2),
		},
		LockCapable: true, OverrideAllowed: true,
		Description: "Days from delivery to seller-wallet credit",
		RegisteredIn: "cod", AddedInVersion: "v0",
	},
	{
		Key: KeyCODVerificationMode, ValueType: TypeString,
		DefaultGlobal: StringValue("above_x"),
		Description: "When to verify COD orders pre-pickup: 'always'|'above_x'|'none'",
		RegisteredIn: "cod", AddedInVersion: "v0",
	},
	{
		Key: KeyCODVerificationThresholdInr, ValueType: TypePaise,
		DefaultGlobal: PaiseValue(core.FromRupees(500)),
		Description: "Threshold above which COD verification is required",
		RegisteredIn: "cod", AddedInVersion: "v0",
	},

	// Allocation
	{Key: KeyAllocationWeightCost, ValueType: TypeInt64, DefaultGlobal: Int64Value(100), Description: "Allocation engine weight on cost (bp)", RegisteredIn: "allocation", AddedInVersion: "v0"},
	{Key: KeyAllocationWeightSpeed, ValueType: TypeInt64, DefaultGlobal: Int64Value(50), Description: "Allocation engine weight on speed (bp)", RegisteredIn: "allocation", AddedInVersion: "v0"},
	{Key: KeyAllocationWeightReliability, ValueType: TypeInt64, DefaultGlobal: Int64Value(70), Description: "Allocation engine weight on reliability (bp)", RegisteredIn: "allocation", AddedInVersion: "v0"},
	{Key: KeyAllocationWeightSellerPref, ValueType: TypeInt64, DefaultGlobal: Int64Value(30), Description: "Allocation engine weight on seller preference (bp)", RegisteredIn: "allocation", AddedInVersion: "v0"},
	{Key: KeyAllocationAutoBookMinScoreGap, ValueType: TypeInt64, DefaultGlobal: Int64Value(500), Description: "Min score gap for auto-book (bp)", RegisteredIn: "allocation", AddedInVersion: "v0"},

	// Carriers
	{
		// The allocation engine parses each member of this set as a carrier
		// UUID — historical entries used human-readable codes ("delhivery"),
		// but they silently dropped through ParseCarrierID and produced
		// "no carriers available". Defaults are now UUIDs that match the
		// constants in internal/carriers/ids.go.
		Key: KeyCarriersAllowedSet, ValueType: TypeStringSet,
		DefaultGlobal: StringSetValue(core.NewStringSet("d0d1f1e7-0000-4000-8000-000000000001")),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  StringSetValue(core.NewStringSet("d0d1f1e7-0000-4000-8000-000000000001")),
			core.SellerTypeEnterprise: StringSetValue(core.NewStringSet("d0d1f1e7-0000-4000-8000-000000000001")),
		},
		Description: "Set of carrier UUIDs this seller can route to",
		RegisteredIn: "carriers", AddedInVersion: "v0",
	},
	{Key: KeyCarriersExcludedSet, ValueType: TypeStringSet, DefaultGlobal: StringSetValue(core.NewStringSet()), Description: "Carriers this seller has explicitly excluded", RegisteredIn: "carriers", AddedInVersion: "v0"},

	// Delivery
	{Key: KeyDeliveryMaxAttempts, ValueType: TypeInt64, DefaultGlobal: Int64Value(2), LockCapable: true, OverrideAllowed: true, Description: "Max forward delivery attempts before RTO", RegisteredIn: "ndr", AddedInVersion: "v0"},
	{Key: KeyDeliveryReattemptWindowHours, ValueType: TypeInt64, DefaultGlobal: Int64Value(24), LockCapable: true, OverrideAllowed: true, Description: "Hours between reattempts", RegisteredIn: "ndr", AddedInVersion: "v0"},
	{Key: KeyDeliveryAutoRTOOnMax, ValueType: TypeBool, DefaultGlobal: BoolValue(true), LockCapable: true, OverrideAllowed: true, Description: "Auto-trigger RTO when max attempts reached", RegisteredIn: "ndr", AddedInVersion: "v0"},

	// Pricing
	{Key: KeyPricingRateCardRef, ValueType: TypeString, DefaultGlobal: StringValue(""), Description: "UUID of the rate card for this seller", RegisteredIn: "pricing", AddedInVersion: "v0"},

	// Buyer experience
	{Key: KeyBuyerExpBrandLogoURL, ValueType: TypeString, DefaultGlobal: StringValue(""), Description: "URL of seller's logo for buyer pages", RegisteredIn: "buyerexp", AddedInVersion: "v0"},
	{Key: KeyBuyerExpCustomDomain, ValueType: TypeString, DefaultGlobal: StringValue(""), Description: "Custom tracking domain", RegisteredIn: "buyerexp", AddedInVersion: "v0"},

	// Features
	{
		Key: KeyFeatureInsurance, ValueType: TypeBool,
		DefaultGlobal:   BoolValue(false),
		LockCapable:     true,
		OverrideAllowed: true,
		Description:     "Insurance attach available for this seller",
		RegisteredIn:    "insurance", AddedInVersion: "v0",
	},
	{
		Key: KeyFeatureWeightDisputeAuto, ValueType: TypeBool,
		DefaultGlobal: BoolValue(false),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  BoolValue(true),
			core.SellerTypeEnterprise: BoolValue(true),
		},
		LockCapable: true, OverrideAllowed: true,
		Description:  "Auto weight-dispute filing on this seller's behalf",
		RegisteredIn: "recon", AddedInVersion: "v0",
	},

	// Usage limits — enforced at runtime by guards.
	// 0 means unlimited; positive value caps the rolling window.
	{
		Key: KeyShipmentsPerMonthLimit, ValueType: TypeInt64,
		DefaultGlobal: Int64Value(500), // small_business default cap
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  Int64Value(10_000),
			core.SellerTypeEnterprise: Int64Value(0), // unlimited for enterprise
		},
		LockCapable: true, OverrideAllowed: true,
		Description:    "Hard cap on shipments per calendar month; 0 = unlimited",
		RegisteredIn:   "shipments", AddedInVersion: "v0",
	},
	{
		Key: KeyOrdersPerDayLimit, ValueType: TypeInt64,
		DefaultGlobal: Int64Value(200),
		DefaultsByType: map[core.SellerType]Value{
			core.SellerTypeMidMarket:  Int64Value(2_000),
			core.SellerTypeEnterprise: Int64Value(0),
		},
		LockCapable: true, OverrideAllowed: true,
		Description:    "Hard cap on orders created per calendar day; 0 = unlimited",
		RegisteredIn:   "orders", AddedInVersion: "v0",
	},
	{
		Key: KeyContractActiveID, ValueType: TypeString,
		DefaultGlobal:   StringValue(""),
		OverrideAllowed: true,
		Description:     "Currently active contract ID for this seller (empty = no contract)",
		RegisteredIn:    "contracts", AddedInVersion: "v0",
	},
}

// DefinitionByKey returns the registered Definition for key, or nil.
func DefinitionByKey(key Key) *Definition {
	for i := range Definitions {
		if Definitions[i].Key == key {
			return &Definitions[i]
		}
	}
	return nil
}
