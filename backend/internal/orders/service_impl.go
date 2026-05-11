package orders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
	"github.com/vishal1132/pikshipp/backend/internal/outbox"
)

type serviceImpl struct {
	pool   *pgxpool.Pool
	outbox *outbox.Emitter
	log    *slog.Logger
}

// New constructs the orders service. pool MUST be the app pool — every method
// runs inside a seller-scoped tx so RLS policies on order_record /
// order_line / order_state_event resolve correctly via app.current_seller_id().
func New(pool *pgxpool.Pool, ob *outbox.Emitter, log *slog.Logger) Service {
	return &serviceImpl{pool: pool, outbox: ob, log: log}
}

func (s *serviceImpl) Create(ctx context.Context, req CreateRequest) (Order, error) {
	if len(req.Lines) == 0 {
		return Order{}, fmt.Errorf("%w: order must have at least one line", core.ErrInvalidArgument)
	}
	var out Order
	err := dbtx.WithSellerTx(ctx, s.pool, req.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		o, err := insertOrder(ctx, tx, req)
		if err != nil {
			return err
		}
		out = o
		return nil
	})
	if err != nil {
		return Order{}, fmt.Errorf("orders.Create: %w", err)
	}
	return out, nil
}

func (s *serviceImpl) Get(ctx context.Context, sellerID core.SellerID, id core.OrderID) (Order, error) {
	var out Order
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		o, err := getOrder(ctx, tx, sellerID, id)
		if err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

func (s *serviceImpl) GetByChannelRef(ctx context.Context, sellerID core.SellerID, channel, channelOrderID string) (Order, error) {
	var out Order
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		o, err := getOrderByChannel(ctx, tx, sellerID, channel, channelOrderID)
		if err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

func (s *serviceImpl) Update(ctx context.Context, sellerID core.SellerID, id core.OrderID, patch UpdatePatch) (Order, error) {
	var out Order
	err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		o, err := getOrder(ctx, tx, sellerID, id)
		if err != nil {
			return err
		}
		if o.State != StateDraft && o.State != StateReady {
			return fmt.Errorf("%w: order in state %s cannot be updated", core.ErrInvalidArgument, o.State)
		}
		setSQL := `UPDATE order_record SET updated_at=now()`
		args := []any{}
		argN := 1
		if patch.BuyerPhone != nil {
			setSQL += fmt.Sprintf(", buyer_phone=$%d", argN+1)
			args = append(args, *patch.BuyerPhone)
			argN++
		}
		if patch.Notes != nil {
			setSQL += fmt.Sprintf(", notes=$%d", argN+1)
			args = append(args, *patch.Notes)
			argN++
		}
		if patch.Tags != nil {
			setSQL += fmt.Sprintf(", tags=$%d", argN+1)
			args = append(args, *patch.Tags)
			argN++
		}
		setSQL += fmt.Sprintf(" WHERE id=$%d AND seller_id=$%d", argN+1, argN+2)
		args = append(args, id.UUID(), sellerID.UUID())
		if len(args) > 2 {
			if _, err := tx.Exec(ctx, setSQL, args...); err != nil {
				return fmt.Errorf("orders.Update exec: %w", err)
			}
		}
		updated, err := getOrder(ctx, tx, sellerID, id)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	return out, err
}

// transitionUnder runs a state-machine step inside a seller-scoped tx.
// SELECT … FOR UPDATE serializes concurrent transitions on the same order.
func (s *serviceImpl) transitionUnder(ctx context.Context, sellerID core.SellerID, id core.OrderID, from, to OrderState, reason string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.transitionUnder lock: %w", err)
		}
		if OrderState(state) != from {
			return fmt.Errorf("%w: order is %s not %s", core.ErrInvalidArgument, state, from)
		}
		return transitionState(ctx, tx, sellerID, id, from, to, reason)
	})
}

func (s *serviceImpl) MarkReady(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.transitionUnder(ctx, sellerID, id, StateDraft, StateReady, "marked_ready")
}

func (s *serviceImpl) Cancel(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.Cancel lock: %w", err)
		}
		if !CanTransition(OrderState(state), StateCancelled) {
			return fmt.Errorf("%w: cannot cancel order in state %s", core.ErrInvalidArgument, state)
		}
		if err := cancelOrderRow(ctx, tx, sellerID, id, reason); err != nil {
			return fmt.Errorf("orders.Cancel: %w", err)
		}
		_, err = tx.Exec(ctx, insertStateEventSQL, id.UUID(), sellerID.UUID(), state, string(StateCancelled), reason)
		if err != nil {
			return fmt.Errorf("orders.Cancel state event: %w", err)
		}
		return nil
	})
}

func (s *serviceImpl) MarkAllocating(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.transitionUnder(ctx, sellerID, id, StateReady, StateAllocating, "allocation_started")
}

func (s *serviceImpl) MarkBooked(ctx context.Context, sellerID core.SellerID, id core.OrderID, ref BookedRef) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.MarkBooked lock: %w", err)
		}
		// Allow ready or allocating → booked. The shipment service may book
		// directly from ready when allocation is fabricated client-side.
		if state != string(StateAllocating) && state != string(StateReady) {
			return fmt.Errorf("%w: cannot book order in state %s", core.ErrInvalidArgument, state)
		}
		if err := transitionState(ctx, tx, sellerID, id, OrderState(state), StateBooked, "booked"); err != nil {
			return err
		}
		return bookOrderRow(ctx, tx, sellerID, id, ref)
	})
}

func (s *serviceImpl) MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.transitionUnder(ctx, sellerID, id, StateBooked, StateInTransit, "in_transit")
}

func (s *serviceImpl) MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.transitionUnder(ctx, sellerID, id, StateInTransit, StateDelivered, "delivered")
}

func (s *serviceImpl) MarkRTO(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.MarkRTO lock: %w", err)
		}
		if !CanTransition(OrderState(state), StateRTO) {
			return fmt.Errorf("%w: cannot mark RTO from state %s", core.ErrInvalidArgument, state)
		}
		return transitionState(ctx, tx, sellerID, id, OrderState(state), StateRTO, reason)
	})
}

// MarkPaid records that a prepaid order's payment has reflected. Idempotent
// — clicking twice doesn't churn paid_at. Refuses to mark COD orders (the
// courier handles collection there) or already-refunded ones.
func (s *serviceImpl) MarkPaid(ctx context.Context, sellerID core.SellerID, id core.OrderID, ref MarkPaidRef) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var paymentMethod, paymentStatus string
		err := tx.QueryRow(ctx, `SELECT payment_method, payment_status FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&paymentMethod, &paymentStatus)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.MarkPaid lock: %w", err)
		}
		if paymentMethod != string(core.PaymentModePrepaid) {
			return fmt.Errorf("%w: cannot mark COD order as paid (handled by carrier)", core.ErrInvalidArgument)
		}
		if paymentStatus == "refunded" {
			return fmt.Errorf("%w: order is already refunded", core.ErrInvalidArgument)
		}
		if paymentStatus == "paid" {
			return nil // idempotent — already paid
		}
		var paidBy *string
		if ref.PaidByUserID != nil {
			s := ref.PaidByUserID.String()
			paidBy = &s
		}
		_, err = tx.Exec(ctx, `
            UPDATE order_record SET
                payment_status = 'paid',
                paid_at = COALESCE(paid_at, now()),
                paid_reference = $2,
                paid_by_user_id = $3,
                updated_at = now()
            WHERE id = $1 AND seller_id = $4`,
			id.UUID(), ref.Reference, paidBy, sellerID.UUID())
		return err
	})
}

func (s *serviceImpl) Close(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM order_record WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("orders.Close lock: %w", err)
		}
		if !CanTransition(OrderState(state), StateClosed) {
			return fmt.Errorf("%w: cannot close order in state %s", core.ErrInvalidArgument, state)
		}
		return transitionState(ctx, tx, sellerID, id, OrderState(state), StateClosed, "closed")
	})
}

func (s *serviceImpl) List(ctx context.Context, q ListQuery) (ListResult, error) {
	var result ListResult
	err := dbtx.WithReadOnlyTx(ctx, s.pool, q.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		ids, err := listOrderIDs(ctx, tx, q.SellerID, q.Limit, q.Offset, q.States)
		if err != nil {
			return fmt.Errorf("orders.List: %w", err)
		}
		result.Orders = make([]Order, 0, len(ids))
		for _, id := range ids {
			o, err := getOrder(ctx, tx, q.SellerID, id)
			if err != nil {
				continue
			}
			result.Orders = append(result.Orders, o)
		}
		result.Total = len(result.Orders)
		return nil
	})
	return result, err
}
