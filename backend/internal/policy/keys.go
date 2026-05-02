package policy

// Key constants for all registered policy settings. Keep alphabetized within sections.

const (
	// Wallet
	KeyWalletPosture             Key = "wallet.posture"
	KeyWalletCreditLimitInr      Key = "wallet.credit_limit_inr"
	KeyWalletGraceNegativeAmount Key = "wallet.grace_negative_amount_inr"

	// COD
	KeyCODEnabled                  Key = "cod.enabled"
	KeyCODRemittanceCycleDays      Key = "cod.remittance_cycle_days"
	KeyCODVerificationMode         Key = "cod.verification_mode"
	KeyCODVerificationThresholdInr Key = "cod.verification_threshold_inr"

	// Allocation
	KeyAllocationWeightCost          Key = "allocation.weight_cost"
	KeyAllocationWeightSpeed         Key = "allocation.weight_speed"
	KeyAllocationWeightReliability   Key = "allocation.weight_reliability"
	KeyAllocationWeightSellerPref    Key = "allocation.weight_seller_pref"
	KeyAllocationAutoBookMinScoreGap Key = "allocation.auto_book_min_score_gap"

	// Carriers
	KeyCarriersAllowedSet  Key = "carriers.allowed_set"
	KeyCarriersExcludedSet Key = "carriers.excluded_set"

	// Delivery
	KeyDeliveryMaxAttempts          Key = "delivery.max_attempts"
	KeyDeliveryReattemptWindowHours Key = "delivery.reattempt_window_hours"
	KeyDeliveryAutoRTOOnMax         Key = "delivery.auto_rto_on_max"

	// Pricing
	KeyPricingRateCardRef Key = "pricing.rate_card_ref"

	// Buyer experience
	KeyBuyerExpBrandLogoURL Key = "buyer_experience.brand.logo_url"
	KeyBuyerExpCustomDomain Key = "buyer_experience.custom_domain"

	// Features
	KeyFeatureInsurance         Key = "features.insurance"
	KeyFeatureWeightDisputeAuto Key = "features.weight_dispute_auto"

	// Usage limits (enforced at runtime)
	KeyShipmentsPerMonthLimit Key = "limits.shipments_per_month"
	KeyOrdersPerDayLimit      Key = "limits.orders_per_day"

	// Enterprise / contract feature gates
	KeyContractActiveID Key = "contract.active_id"
)
