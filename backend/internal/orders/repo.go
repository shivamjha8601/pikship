package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// querier is the subset of pgx that both *pgxpool.Pool and pgx.Tx satisfy.
// All repo methods take a querier so the service layer can decide whether
// to run them against a shared seller-scoped tx (RLS enforced) or the pool.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	insertOrderSQL = `
        INSERT INTO order_record (
            seller_id, state, channel, channel_order_id, order_ref,
            buyer_name, buyer_phone, buyer_email, billing_address, shipping_address,
            shipping_pincode, shipping_state, payment_method,
            subtotal_paise, shipping_paise, discount_paise, tax_paise, total_paise, cod_amount_paise,
            pickup_location_id, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
            notes, tags
        ) VALUES (
            $1,'draft',$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10,$11,$12,
            $13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25
        ) RETURNING id, created_at, updated_at
    `
	insertOrderLineSQL = `
        INSERT INTO order_line
            (order_id, seller_id, line_no, sku, name, quantity, unit_price_paise, unit_weight_g, hsn_code, category_hint)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `
	insertStateEventSQL = `
        INSERT INTO order_state_event (order_id, seller_id, from_state, to_state, reason)
        VALUES ($1,$2,$3,$4,$5)
    `
	getOrderSQL = `
        SELECT id, seller_id, state, channel, channel_order_id, COALESCE(order_ref,''),
               buyer_name, buyer_phone, COALESCE(buyer_email,''),
               billing_address, shipping_address, shipping_pincode, shipping_state,
               payment_method, subtotal_paise, shipping_paise, discount_paise,
               tax_paise, total_paise, cod_amount_paise,
               pickup_location_id, package_weight_g, package_length_mm,
               package_width_mm, package_height_mm,
               COALESCE(awb_number,''), COALESCE(carrier_code,''),
               booked_at, shipped_at, out_for_delivery_at, delivered_at, cancelled_at,
               COALESCE(notes,''), COALESCE(tags,'{}'), created_at, updated_at
        FROM order_record WHERE id = $1 AND seller_id = $2
    `
	getOrderByChannelSQL = `
        SELECT id, seller_id, state, channel, channel_order_id, COALESCE(order_ref,''),
               buyer_name, buyer_phone, COALESCE(buyer_email,''),
               billing_address, shipping_address, shipping_pincode, shipping_state,
               payment_method, subtotal_paise, shipping_paise, discount_paise,
               tax_paise, total_paise, cod_amount_paise,
               pickup_location_id, package_weight_g, package_length_mm,
               package_width_mm, package_height_mm,
               COALESCE(awb_number,''), COALESCE(carrier_code,''),
               booked_at, shipped_at, out_for_delivery_at, delivered_at, cancelled_at,
               COALESCE(notes,''), COALESCE(tags,'{}'), created_at, updated_at
        FROM order_record WHERE seller_id = $1 AND channel = $2 AND channel_order_id = $3
    `
	getLinesSQL = `
        SELECT line_no, sku, name, quantity, unit_price_paise, unit_weight_g,
               COALESCE(hsn_code,''), COALESCE(category_hint,'')
        FROM order_line WHERE order_id = $1 ORDER BY line_no
    `
	updateStateSQL = `
        UPDATE order_record SET state = $2, updated_at = now() WHERE id = $1 AND seller_id = $3
    `
	bookOrderSQL = `
        UPDATE order_record SET state = 'booked', awb_number = $2, carrier_code = $3, booked_at = now(), updated_at = now()
        WHERE id = $1 AND seller_id = $4
    `
	cancelOrderSQL = `
        UPDATE order_record SET state = 'cancelled', cancelled_at = now(), cancelled_reason = $2, updated_at = now()
        WHERE id = $1 AND seller_id = $3
    `
	listOrdersSQL = `
        SELECT id FROM order_record
        WHERE seller_id = $1
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3
    `
)

func insertOrder(ctx context.Context, q querier, req CreateRequest) (Order, error) {
	billingJSON, _ := json.Marshal(req.BillingAddress)
	shippingJSON, _ := json.Marshal(req.ShippingAddress)
	tagsArr := req.Tags
	if tagsArr == nil {
		tagsArr = []string{}
	}

	var id uuid.UUID
	var createdAt, updatedAt time.Time
	err := q.QueryRow(ctx, insertOrderSQL,
		req.SellerID.UUID(),
		req.Channel, req.ChannelOrderID, req.OrderRef,
		req.BuyerName, req.BuyerPhone, req.BuyerEmail,
		billingJSON, shippingJSON,
		string(req.ShippingPincode), req.ShippingState, string(req.PaymentMethod),
		int64(req.SubtotalPaise), int64(req.ShippingPaise), int64(req.DiscountPaise),
		int64(req.TaxPaise), int64(req.TotalPaise), int64(req.CODAmountPaise),
		req.PickupLocationID.UUID(),
		req.PackageWeightG, req.PackageLengthMM, req.PackageWidthMM, req.PackageHeightMM,
		req.Notes, tagsArr,
	).Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		return Order{}, fmt.Errorf("orders.insertOrder: %w", err)
	}

	for i, line := range req.Lines {
		_, err := q.Exec(ctx, insertOrderLineSQL,
			id, req.SellerID.UUID(), i+1,
			line.SKU, line.Name, line.Quantity,
			int64(line.UnitPricePaise), line.UnitWeightG,
			line.HSNCode, line.CategoryHint,
		)
		if err != nil {
			return Order{}, fmt.Errorf("orders.insertLine: %w", err)
		}
	}

	return getOrder(ctx, q, req.SellerID, core.OrderIDFromUUID(id))
}

func getOrder(ctx context.Context, q querier, sellerID core.SellerID, id core.OrderID) (Order, error) {
	o, err := scanOrder(q.QueryRow(ctx, getOrderSQL, id.UUID(), sellerID.UUID()))
	if err != nil {
		return Order{}, err
	}
	o.Lines, err = getLines(ctx, q, id)
	return o, err
}

func getOrderByChannel(ctx context.Context, q querier, sellerID core.SellerID, channel, channelOrderID string) (Order, error) {
	o, err := scanOrder(q.QueryRow(ctx, getOrderByChannelSQL, sellerID.UUID(), channel, channelOrderID))
	if err != nil {
		return Order{}, err
	}
	o.Lines, err = getLines(ctx, q, o.ID)
	return o, err
}

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	var id, sellerID, pickupID uuid.UUID
	var billingJSON, shippingJSON []byte
	var state, paymentMethod, pincode, shippingState string
	var sub, ship, disc, tax, total, cod int64
	var weightG, lenMM, widMM, heiMM int
	var tags []string
	if err := row.Scan(
		&id, &sellerID, &state, &o.Channel, &o.ChannelOrderID, &o.OrderRef,
		&o.BuyerName, &o.BuyerPhone, &o.BuyerEmail,
		&billingJSON, &shippingJSON, &pincode, &shippingState,
		&paymentMethod, &sub, &ship, &disc, &tax, &total, &cod,
		&pickupID, &weightG, &lenMM, &widMM, &heiMM,
		&o.AWBNumber, &o.CarrierCode,
		&o.BookedAt, &o.ShippedAt, &o.OutForDeliveryAt, &o.DeliveredAt, &o.CancelledAt,
		&o.Notes, &tags, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Order{}, core.ErrNotFound
		}
		return Order{}, fmt.Errorf("orders.scanOrder: %w", err)
	}
	o.ID = core.OrderIDFromUUID(id)
	o.SellerID = core.SellerIDFromUUID(sellerID)
	o.State = OrderState(state)
	o.PaymentMethod = core.PaymentMode(paymentMethod)
	o.ShippingPincode = core.Pincode(pincode)
	o.ShippingState = shippingState
	o.SubtotalPaise, o.ShippingPaise = core.Paise(sub), core.Paise(ship)
	o.DiscountPaise, o.TaxPaise = core.Paise(disc), core.Paise(tax)
	o.TotalPaise, o.CODAmountPaise = core.Paise(total), core.Paise(cod)
	o.PickupLocationID = core.PickupLocationIDFromUUID(pickupID)
	o.PackageWeightG, o.PackageLengthMM = weightG, lenMM
	o.PackageWidthMM, o.PackageHeightMM = widMM, heiMM
	o.Tags = tags
	_ = json.Unmarshal(billingJSON, &o.BillingAddress)
	_ = json.Unmarshal(shippingJSON, &o.ShippingAddress)
	return o, nil
}

func getLines(ctx context.Context, q querier, orderID core.OrderID) ([]OrderLine, error) {
	rows, err := q.Query(ctx, getLinesSQL, orderID.UUID())
	if err != nil {
		return nil, fmt.Errorf("orders.getLines: %w", err)
	}
	defer rows.Close()
	var lines []OrderLine
	for rows.Next() {
		var l OrderLine
		var price int64
		if err := rows.Scan(&l.LineNo, &l.SKU, &l.Name, &l.Quantity, &price, &l.UnitWeightG, &l.HSNCode, &l.CategoryHint); err != nil {
			return nil, fmt.Errorf("orders.getLines scan: %w", err)
		}
		l.UnitPricePaise = core.Paise(price)
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// transitionState performs a same-tx state change + state event insert.
// Caller must already have validated the source state under FOR UPDATE.
// Side effect: stamps shipped_at / out_for_delivery_at / delivered_at on the
// matching state milestones so downstream surfaces (notifications, reports,
// the UI's "delivered 2h ago") don't have to derive timestamps from
// tracking_event.
func transitionState(ctx context.Context, q querier, sellerID core.SellerID, id core.OrderID, from, to OrderState, reason string) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s→%s not allowed", core.ErrInvalidArgument, from, to)
	}
	stampCol := ""
	switch to {
	case StateInTransit:
		stampCol = "shipped_at"
	case StateDelivered:
		stampCol = "delivered_at"
	}
	if stampCol != "" {
		sql := fmt.Sprintf(`UPDATE order_record SET state = $2, %s = COALESCE(%s, now()), updated_at = now() WHERE id = $1 AND seller_id = $3`, stampCol, stampCol)
		if _, err := q.Exec(ctx, sql, id.UUID(), string(to), sellerID.UUID()); err != nil {
			return fmt.Errorf("orders.transitionState: %w", err)
		}
	} else {
		if _, err := q.Exec(ctx, updateStateSQL, id.UUID(), string(to), sellerID.UUID()); err != nil {
			return fmt.Errorf("orders.transitionState: %w", err)
		}
	}
	if _, err := q.Exec(ctx, insertStateEventSQL, id.UUID(), sellerID.UUID(), string(from), string(to), reason); err != nil {
		return fmt.Errorf("orders.transitionState event: %w", err)
	}
	return nil
}

func bookOrderRow(ctx context.Context, q querier, sellerID core.SellerID, id core.OrderID, ref BookedRef) error {
	_, err := q.Exec(ctx, bookOrderSQL, id.UUID(), ref.AWBNumber, ref.CarrierCode, sellerID.UUID())
	return err
}

func cancelOrderRow(ctx context.Context, q querier, sellerID core.SellerID, id core.OrderID, reason string) error {
	_, err := q.Exec(ctx, cancelOrderSQL, id.UUID(), reason, sellerID.UUID())
	return err
}

func listOrderIDs(ctx context.Context, q querier, sellerID core.SellerID, limit, offset int) ([]core.OrderID, error) {
	if limit == 0 {
		limit = 50
	}
	rows, err := q.Query(ctx, listOrdersSQL, sellerID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("orders.listOrderIDs: %w", err)
	}
	defer rows.Close()
	var ids []core.OrderID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, core.OrderIDFromUUID(id))
	}
	return ids, rows.Err()
}
