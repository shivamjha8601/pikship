// Package buyerexp provides the public buyer-facing tracking experience.
// Per LLD §03-services/21-buyer-experience.
package buyerexp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
)

// TrackingView is the buyer-facing view of a shipment.
type TrackingView struct {
	AWB         string
	CarrierName string
	State       string
	Events      []tracking.Event
	SellerName  string
	LogoURL     string
}

// Service manages public tracking tokens and the tracking page.
type Service interface {
	IssueToken(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, ttl time.Duration) (string, error)
	GetTrackingView(ctx context.Context, token string) (TrackingView, error)
	GetByAWB(ctx context.Context, awb string) (TrackingView, error)
}

type service struct {
	pool     *pgxpool.Pool
	tracking tracking.Service
}

// New constructs the buyer experience service.
func New(pool *pgxpool.Pool, tr tracking.Service) Service {
	return &service{pool: pool, tracking: tr}
}

func (s *service) IssueToken(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, ttl time.Duration) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("buyerexp.IssueToken: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	var expiresAt *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expiresAt = &t
	}
	_, err := s.pool.Exec(ctx, `
        INSERT INTO tracking_public_token (token, shipment_id, seller_id, expires_at)
        VALUES ($1,$2,$3,$4) ON CONFLICT (token) DO NOTHING`,
		token, shipmentID.UUID(), sellerID.UUID(), expiresAt)
	if err != nil {
		return "", fmt.Errorf("buyerexp.IssueToken: %w", err)
	}
	return token, nil
}

func (s *service) GetTrackingView(ctx context.Context, token string) (TrackingView, error) {
	var shipmentID core.ShipmentID
	var sellerID core.SellerID
	var expiresAt *time.Time
	if err := s.pool.QueryRow(ctx, `
        SELECT shipment_id, seller_id, expires_at
        FROM tracking_public_token WHERE token=$1`, token,
	).Scan(&shipmentID, &sellerID, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackingView{}, core.ErrNotFound
		}
		return TrackingView{}, fmt.Errorf("buyerexp.GetTrackingView: %w", err)
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return TrackingView{}, fmt.Errorf("%w: tracking token expired", core.ErrNotFound)
	}
	events, err := s.tracking.ListEventsByShipment(ctx, sellerID, shipmentID)
	if err != nil {
		return TrackingView{}, err
	}

	var awb, state, carrier string
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(awb,''), state, carrier_code FROM shipment WHERE id=$1`,
		shipmentID.UUID()).Scan(&awb, &state, &carrier)

	return TrackingView{AWB: awb, CarrierName: carrier, State: state, Events: events}, nil
}

func (s *service) GetByAWB(ctx context.Context, awb string) (TrackingView, error) {
	var shipmentID core.ShipmentID
	var sellerID core.SellerID
	var state, carrier string
	if err := s.pool.QueryRow(ctx, `SELECT id, seller_id, state, carrier_code FROM shipment WHERE awb=$1`, awb).
		Scan(&shipmentID, &sellerID, &state, &carrier); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrackingView{}, core.ErrNotFound
		}
		return TrackingView{}, fmt.Errorf("buyerexp.GetByAWB: %w", err)
	}
	events, _ := s.tracking.ListEventsByShipment(ctx, sellerID, shipmentID)
	return TrackingView{AWB: awb, CarrierName: carrier, State: state, Events: events}, nil
}
