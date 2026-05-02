// Package support manages seller support tickets.
// Per LLD §03-services/22-support.
package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Ticket is a support ticket.
type Ticket struct {
	ID         core.TicketID
	SellerID   core.SellerID
	CreatedBy  core.UserID
	Subject    string
	Body       string
	Category   string
	State      string
	ShipmentID *core.ShipmentID
	OrderID    *core.OrderID
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Service manages support tickets.
type Service interface {
	Open(ctx context.Context, sellerID core.SellerID, createdBy core.UserID, subject, body, category string, shipmentID *core.ShipmentID, orderID *core.OrderID) (Ticket, error)
	Close(ctx context.Context, sellerID core.SellerID, id core.TicketID, resolution string) error
	Get(ctx context.Context, sellerID core.SellerID, id core.TicketID) (Ticket, error)
	List(ctx context.Context, sellerID core.SellerID, state string) ([]Ticket, error)
}

type service struct{ pool *pgxpool.Pool }

// New constructs the support service.
func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

func (s *service) Open(ctx context.Context, sellerID core.SellerID, createdBy core.UserID, subject, body, category string, shipmentID *core.ShipmentID, orderID *core.OrderID) (Ticket, error) {
	id := uuid.New()
	var shipUUID, orderUUID *uuid.UUID
	if shipmentID != nil {
		u := shipmentID.UUID()
		shipUUID = &u
	}
	if orderID != nil {
		u := orderID.UUID()
		orderUUID = &u
	}
	var createdAt, updatedAt time.Time
	if err := s.pool.QueryRow(ctx, `
        INSERT INTO support_ticket (id, seller_id, created_by, subject, body, category, state, shipment_id, order_id)
        VALUES ($1,$2,$3,$4,$5,$6,'open',$7,$8)
        RETURNING created_at, updated_at`,
		id, sellerID.UUID(), createdBy.UUID(), subject, body, category, shipUUID, orderUUID,
	).Scan(&createdAt, &updatedAt); err != nil {
		return Ticket{}, fmt.Errorf("support.Open: %w", err)
	}
	return Ticket{
		ID: core.TicketIDFromUUID(id), SellerID: sellerID, CreatedBy: createdBy,
		Subject: subject, Body: body, Category: category, State: "open",
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func (s *service) Close(ctx context.Context, sellerID core.SellerID, id core.TicketID, resolution string) error {
	_, err := s.pool.Exec(ctx, `UPDATE support_ticket SET state='closed', resolution=$2, updated_at=now() WHERE id=$1 AND seller_id=$3`,
		id.UUID(), resolution, sellerID.UUID())
	return err
}

func (s *service) Get(ctx context.Context, sellerID core.SellerID, id core.TicketID) (Ticket, error) {
	var t Ticket
	var tid, sellID, createdBy uuid.UUID
	if err := s.pool.QueryRow(ctx, `
        SELECT id, seller_id, created_by, subject, body, category, state, created_at, updated_at
        FROM support_ticket WHERE id=$1 AND seller_id=$2`, id.UUID(), sellerID.UUID(),
	).Scan(&tid, &sellID, &createdBy, &t.Subject, &t.Body, &t.Category, &t.State, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Ticket{}, core.ErrNotFound
		}
		return Ticket{}, fmt.Errorf("support.Get: %w", err)
	}
	t.ID = core.TicketIDFromUUID(tid)
	t.SellerID = core.SellerIDFromUUID(sellID)
	t.CreatedBy = core.UserIDFromUUID(createdBy)
	return t, nil
}

func (s *service) List(ctx context.Context, sellerID core.SellerID, state string) ([]Ticket, error) {
	q := `SELECT id, seller_id, created_by, subject, body, category, state, created_at, updated_at FROM support_ticket WHERE seller_id=$1`
	args := []any{sellerID.UUID()}
	if state != "" {
		q += " AND state=$2"
		args = append(args, state)
	}
	q += " ORDER BY created_at DESC LIMIT 50"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("support.List: %w", err)
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		var tid, sellID, createdBy uuid.UUID
		if err := rows.Scan(&tid, &sellID, &createdBy, &t.Subject, &t.Body, &t.Category, &t.State, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.ID = core.TicketIDFromUUID(tid)
		t.SellerID = core.SellerIDFromUUID(sellID)
		t.CreatedBy = core.UserIDFromUUID(createdBy)
		out = append(out, t)
	}
	return out, rows.Err()
}
