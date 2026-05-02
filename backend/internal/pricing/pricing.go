// Package pricing computes shipment costs from rate cards.
//
// The core hot path is QuoteAll, called by the allocation engine for every
// order. It resolves the applicable rate card (seller override > seller-type
// > pikshipp global), looks up the zone, finds the slab, applies adjustments,
// and returns a Quote per (carrier, service) pair.
//
// Per LLD §03-services/06-pricing.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Engine is the public API of the pricing module.
type Engine interface {
	Quote(ctx context.Context, in QuoteInput) (Quote, error)
	QuoteAll(ctx context.Context, in QuoteAllInput) ([]Quote, error)
}

// QuoteInput is one (order, carrier, service) pricing request.
type QuoteInput struct {
	SellerID        core.SellerID
	PickupPincode   core.Pincode
	ShipToPincode   core.Pincode
	CarrierID       core.CarrierID
	ServiceType     core.ServiceType
	DeclaredWeightG int
	LengthMM        int
	WidthMM         int
	HeightMM        int
	PaymentMode     core.PaymentMode
	DeclaredValue   core.Paise
}

// QuoteAllInput batches across multiple carriers.
type QuoteAllInput struct {
	SellerID        core.SellerID
	PickupPincode   core.Pincode
	ShipToPincode   core.Pincode
	DeclaredWeightG int
	LengthMM        int
	WidthMM         int
	HeightMM        int
	PaymentMode     core.PaymentMode
	DeclaredValue   core.Paise
	Candidates      []CarrierCandidate
}

// CarrierCandidate is one (carrier, service) option to price.
type CarrierCandidate struct {
	CarrierID   core.CarrierID
	ServiceType core.ServiceType
}

// Quote is the result for one (carrier, service) pair.
type Quote struct {
	CarrierID     core.CarrierID
	ServiceType   core.ServiceType
	EstimatedDays int
	TotalPaise    core.Paise
	Breakdown     map[string]core.Paise
	RateCardID    core.RateCardID
	Zone          string
}

// --- implementation ---

type engine struct{ pool *pgxpool.Pool }

// New constructs the pricing engine. pool must be the admin pool (reads
// rate cards across all scopes, bypassing RLS).
func New(pool *pgxpool.Pool) Engine { return &engine{pool: pool} }

func (e *engine) Quote(ctx context.Context, in QuoteInput) (Quote, error) {
	card, err := e.resolveCard(ctx, in.SellerID, in.CarrierID, in.ServiceType)
	if err != nil {
		return Quote{}, fmt.Errorf("pricing.Quote: resolve card: %w", err)
	}
	return e.computeQuote(ctx, card, in)
}

func (e *engine) QuoteAll(ctx context.Context, in QuoteAllInput) ([]Quote, error) {
	quotes := make([]Quote, 0, len(in.Candidates))
	for _, c := range in.Candidates {
		q, err := e.Quote(ctx, QuoteInput{
			SellerID:        in.SellerID,
			PickupPincode:   in.PickupPincode,
			ShipToPincode:   in.ShipToPincode,
			CarrierID:       c.CarrierID,
			ServiceType:     c.ServiceType,
			DeclaredWeightG: in.DeclaredWeightG,
			LengthMM:        in.LengthMM,
			WidthMM:         in.WidthMM,
			HeightMM:        in.HeightMM,
			PaymentMode:     in.PaymentMode,
			DeclaredValue:   in.DeclaredValue,
		})
		if err != nil {
			// Skip unavailable carriers rather than failing all.
			continue
		}
		quotes = append(quotes, q)
	}
	return quotes, nil
}

// --- SQL ---

const resolveCardSQL = `
    SELECT id, carrier_id, service_type FROM rate_card
    WHERE status = 'published'
      AND carrier_id = $1 AND service_type = $2
      AND (effective_from <= $3)
      AND (effective_to IS NULL OR effective_to > $3)
      AND (
            (scope = 'seller' AND scope_seller_id = $4)
         OR (scope = 'seller_type' AND scope_seller_type = (SELECT seller_type FROM seller WHERE id = $4))
         OR scope = 'pikshipp'
      )
    ORDER BY
        CASE scope WHEN 'seller' THEN 0 WHEN 'seller_type' THEN 1 ELSE 2 END ASC
    LIMIT 1
`

const getZoneSQL = `
    SELECT id, zone_code, pincode_patterns_jsonb, estimated_days
    FROM rate_card_zone WHERE rate_card_id = $1
`

const getSlabSQL = `
    SELECT base_first_slab_paise, additional_per_slab_paise, slab_size_g
    FROM rate_card_slab
    WHERE rate_card_id = $1 AND zone_code = $2 AND payment_mode = $3
      AND weight_min_g <= $4 AND weight_max_g > $4
    LIMIT 1
`

type cardRow struct {
	id          uuid.UUID
	carrierID   uuid.UUID
	serviceType string
}

type zoneRow struct {
	id              uuid.UUID
	zoneCode        string
	pincodePatterns []string
	estimatedDays   int
}

func (e *engine) resolveCard(ctx context.Context, sellerID core.SellerID, carrierID core.CarrierID, serviceType core.ServiceType) (cardRow, error) {
	var c cardRow
	err := e.pool.QueryRow(ctx, resolveCardSQL,
		carrierID.UUID(), string(serviceType), time.Now(), sellerID.UUID(),
	).Scan(&c.id, &c.carrierID, &c.serviceType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardRow{}, fmt.Errorf("no published rate card for %s/%s: %w", carrierID, serviceType, core.ErrNotFound)
		}
		return cardRow{}, fmt.Errorf("pricing.resolveCard: %w", err)
	}
	return c, nil
}

func (e *engine) computeQuote(ctx context.Context, card cardRow, in QuoteInput) (Quote, error) {
	// Find the zone that matches shipToPincode.
	rows, err := e.pool.Query(ctx, getZoneSQL, card.id)
	if err != nil {
		return Quote{}, fmt.Errorf("pricing.computeQuote: zones: %w", err)
	}
	defer rows.Close()

	var zone zoneRow
	for rows.Next() {
		var zr zoneRow
		var patternsJSON []byte
		if err := rows.Scan(&zr.id, &zr.zoneCode, &patternsJSON, &zr.estimatedDays); err != nil {
			return Quote{}, err
		}
		_ = json.Unmarshal(patternsJSON, &zr.pincodePatterns)
		for _, pat := range zr.pincodePatterns {
			if matched, _ := regexp.MatchString("^"+pat+"$", string(in.ShipToPincode)); matched {
				zone = zr
				break
			}
		}
		if zone.zoneCode != "" {
			break
		}
	}
	rows.Close()
	if zone.zoneCode == "" {
		return Quote{}, fmt.Errorf("pricing: no zone matched pincode %s", in.ShipToPincode)
	}

	// Volumetric weight = (L×W×H)/5000.
	volWeightG := (in.LengthMM * in.WidthMM * in.HeightMM) / 5_000_000 * 1000
	chargeableG := in.DeclaredWeightG
	if volWeightG > chargeableG {
		chargeableG = volWeightG
	}

	// Get slab.
	var basePaise, additionalPaise int64
	var slabSizeG int
	err = e.pool.QueryRow(ctx, getSlabSQL,
		card.id, zone.zoneCode, string(in.PaymentMode), chargeableG,
	).Scan(&basePaise, &additionalPaise, &slabSizeG)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Quote{}, fmt.Errorf("pricing: no slab matched weight %dg zone %s", chargeableG, zone.zoneCode)
		}
		return Quote{}, fmt.Errorf("pricing.computeQuote slab: %w", err)
	}

	// Compute cost: base + additional slabs above first.
	firstSlabMax := slabSizeG
	extraG := chargeableG - firstSlabMax
	extraSlabs := int64(0)
	if extraG > 0 && slabSizeG > 0 {
		extraSlabs = int64(extraG) / int64(slabSizeG)
		if int64(extraG)%int64(slabSizeG) > 0 {
			extraSlabs++
		}
	}
	total := core.Paise(basePaise + additionalPaise*extraSlabs)

	return Quote{
		CarrierID:     core.CarrierIDFromUUID(card.carrierID),
		ServiceType:   core.ServiceType(card.serviceType),
		EstimatedDays: zone.estimatedDays,
		TotalPaise:    total,
		Breakdown:     map[string]core.Paise{"base": core.Paise(basePaise), "additional": core.Paise(additionalPaise * extraSlabs)},
		RateCardID:    core.RateCardIDFromUUID(card.id),
		Zone:          zone.zoneCode,
	}, nil
}
