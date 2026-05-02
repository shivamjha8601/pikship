package orders

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/outbox"
)

type serviceImpl struct {
	repo   *repo
	outbox *outbox.Emitter
	log    *slog.Logger
}

// New constructs the orders service. pool must be the app pool.
func New(pool *pgxpool.Pool, ob *outbox.Emitter, log *slog.Logger) Service {
	return &serviceImpl{repo: newRepo(pool), outbox: ob, log: log}
}

func (s *serviceImpl) Create(ctx context.Context, req CreateRequest) (Order, error) {
	if len(req.Lines) == 0 {
		return Order{}, fmt.Errorf("%w: order must have at least one line", core.ErrInvalidArgument)
	}
	o, err := s.repo.insertOrder(ctx, req)
	if err != nil {
		return Order{}, fmt.Errorf("orders.Create: %w", err)
	}
	return o, nil
}

func (s *serviceImpl) Get(ctx context.Context, sellerID core.SellerID, id core.OrderID) (Order, error) {
	return s.repo.getOrder(ctx, sellerID, id)
}

func (s *serviceImpl) GetByChannelRef(ctx context.Context, sellerID core.SellerID, channel, channelOrderID string) (Order, error) {
	return s.repo.getOrderByChannel(ctx, sellerID, channel, channelOrderID)
}

func (s *serviceImpl) Update(ctx context.Context, sellerID core.SellerID, id core.OrderID, patch UpdatePatch) (Order, error) {
	o, err := s.repo.getOrder(ctx, sellerID, id)
	if err != nil {
		return Order{}, err
	}
	// Only allow updates in draft/ready states.
	if o.State != StateDraft && o.State != StateReady {
		return Order{}, fmt.Errorf("%w: order in state %s cannot be updated", core.ErrInvalidArgument, o.State)
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

	if len(args) > 2 { // something actually changed
		if _, err := s.repo.pool.Exec(ctx, setSQL, args...); err != nil {
			return Order{}, fmt.Errorf("orders.Update: %w", err)
		}
	}
	return s.repo.getOrder(ctx, sellerID, id)
}

func (s *serviceImpl) MarkReady(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.repo.transition(ctx, sellerID, id, StateDraft, StateReady, "marked_ready")
}

func (s *serviceImpl) Cancel(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error {
	o, err := s.repo.getOrder(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if !CanTransition(o.State, StateCancelled) {
		return fmt.Errorf("%w: cannot cancel order in state %s", core.ErrInvalidArgument, o.State)
	}
	return s.repo.cancelOrder(ctx, sellerID, id, reason)
}

func (s *serviceImpl) MarkAllocating(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.repo.transition(ctx, sellerID, id, StateReady, StateAllocating, "allocation_started")
}

func (s *serviceImpl) MarkBooked(ctx context.Context, sellerID core.SellerID, id core.OrderID, ref BookedRef) error {
	if err := s.repo.transition(ctx, sellerID, id, StateAllocating, StateBooked, "booked"); err != nil {
		return err
	}
	return s.repo.bookOrder(ctx, sellerID, id, ref)
}

func (s *serviceImpl) MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.repo.transition(ctx, sellerID, id, StateBooked, StateInTransit, "in_transit")
}

func (s *serviceImpl) MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	return s.repo.transition(ctx, sellerID, id, StateInTransit, StateDelivered, "delivered")
}

func (s *serviceImpl) MarkRTO(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error {
	o, err := s.repo.getOrder(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if !CanTransition(o.State, StateRTO) {
		return fmt.Errorf("%w: cannot mark RTO from state %s", core.ErrInvalidArgument, o.State)
	}
	return s.repo.transition(ctx, sellerID, id, o.State, StateRTO, reason)
}

func (s *serviceImpl) Close(ctx context.Context, sellerID core.SellerID, id core.OrderID) error {
	o, err := s.repo.getOrder(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if !CanTransition(o.State, StateClosed) {
		return fmt.Errorf("%w: cannot close order in state %s", core.ErrInvalidArgument, o.State)
	}
	return s.repo.transition(ctx, sellerID, id, o.State, StateClosed, "closed")
}

func (s *serviceImpl) List(ctx context.Context, q ListQuery) (ListResult, error) {
	ids, err := s.repo.listOrders(ctx, q.SellerID, q.Limit, q.Offset)
	if err != nil {
		return ListResult{}, fmt.Errorf("orders.List: %w", err)
	}
	result := ListResult{Orders: make([]Order, 0, len(ids))}
	for _, id := range ids {
		o, err := s.repo.getOrder(ctx, q.SellerID, id)
		if err != nil {
			continue
		}
		result.Orders = append(result.Orders, o)
	}
	return result, nil
}
