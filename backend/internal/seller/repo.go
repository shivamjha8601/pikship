package seller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	insertSellerSQL = `
        INSERT INTO seller (
            legal_name, display_name, seller_type, lifecycle_state,
            billing_email, support_email, primary_phone, signup_source, founding_user_id
        ) VALUES ($1,$2,$3,'provisioning',$4,$5,$6,$7,$8)
        RETURNING id, legal_name, display_name, seller_type, lifecycle_state,
                  COALESCE(gstin,''), COALESCE(pan,''), billing_email, support_email,
                  primary_phone, signup_source, founding_user_id,
                  COALESCE(suspended_reason,''), created_at, updated_at
    `

	getSellerSQL = `
        SELECT id, legal_name, display_name, seller_type, lifecycle_state,
               COALESCE(gstin,''), COALESCE(pan,''), billing_email, support_email,
               primary_phone, signup_source, founding_user_id,
               COALESCE(suspended_reason,''), created_at, updated_at
        FROM seller WHERE id = $1
    `

	updateLifecycleSQL = `
        UPDATE seller SET lifecycle_state = $2, updated_at = now() WHERE id = $1
    `
	suspendSQL = `
        UPDATE seller SET
            lifecycle_state = 'suspended',
            suspended_reason = $2,
            suspended_category = $3,
            suspended_at = now(),
            suspended_expires_at = $4,
            updated_at = now()
        WHERE id = $1
    `
	reinstateSQL = `
        UPDATE seller SET
            lifecycle_state = 'active',
            suspended_reason = NULL,
            suspended_category = NULL,
            suspended_at = NULL,
            suspended_expires_at = NULL,
            updated_at = now()
        WHERE id = $1
    `
	windDownSQL = `
        UPDATE seller SET
            lifecycle_state = 'wound_down',
            wound_down_at = now(),
            wound_down_reason = $2,
            updated_at = now()
        WHERE id = $1
    `

	insertLifecycleEventSQL = `
        INSERT INTO seller_lifecycle_event
            (seller_id, from_state, to_state, reason, category, operator_id, payload)
        VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
    `

	upsertKYCSQL = `
        INSERT INTO kyc_application (seller_id, state, legal_name, gstin, pan, business_address, submitted_at)
        VALUES ($1,'submitted',$2,$3,$4,$5::jsonb,now())
        ON CONFLICT (seller_id) DO UPDATE SET
            state = 'submitted',
            legal_name = EXCLUDED.legal_name,
            gstin = EXCLUDED.gstin,
            pan = EXCLUDED.pan,
            business_address = EXCLUDED.business_address,
            submitted_at = now(),
            updated_at = now()
    `
	approveKYCSQL = `
        UPDATE kyc_application SET state='approved', decided_at=now(), decision_reason=$2, verified_by=$3
        WHERE seller_id=$1
    `
	rejectKYCSQL = `
        UPDATE kyc_application SET state='rejected', decided_at=now(), decision_reason=$2
        WHERE seller_id=$1
    `
	getKYCSQL = `
        SELECT seller_id, state, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(pan,''),
               COALESCE(business_address::text,'{}'), submitted_at, decided_at, COALESCE(decision_reason,'')
        FROM kyc_application WHERE seller_id=$1
    `
)

type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

func (r *repo) insertSeller(ctx context.Context, in ProvisionInput) (Seller, error) {
	return r.scanSeller(r.pool.QueryRow(ctx, insertSellerSQL,
		in.LegalName, in.DisplayName, string(in.SellerType),
		in.BillingEmail, in.SupportEmail, in.PrimaryPhone,
		in.SignupSource, in.FoundingUserID.UUID(),
	))
}

func (r *repo) getSeller(ctx context.Context, id core.SellerID) (Seller, error) {
	return r.scanSeller(r.pool.QueryRow(ctx, getSellerSQL, id.UUID()))
}

func (r *repo) scanSeller(row pgx.Row) (Seller, error) {
	var s Seller
	var id, foundingUserID uuid.UUID
	var sellerType, lifecycleState string
	if err := row.Scan(
		&id, &s.LegalName, &s.DisplayName, &sellerType, &lifecycleState,
		&s.GSTIN, &s.PAN, &s.BillingEmail, &s.SupportEmail,
		&s.PrimaryPhone, &s.SignupSource, &foundingUserID,
		&s.SuspendedReason, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Seller{}, core.ErrNotFound
		}
		return Seller{}, fmt.Errorf("seller.scan: %w", err)
	}
	s.ID = core.SellerIDFromUUID(id)
	s.FoundingUserID = core.UserIDFromUUID(foundingUserID)
	s.SellerType = core.SellerType(sellerType)
	s.LifecycleState = LifecycleState(lifecycleState)
	return s, nil
}

func (r *repo) updateLifecycle(ctx context.Context, id core.SellerID, state LifecycleState) error {
	_, err := r.pool.Exec(ctx, updateLifecycleSQL, id.UUID(), string(state))
	return err
}

func (r *repo) suspend(ctx context.Context, id core.SellerID, reason, category string, until *time.Time) error {
	_, err := r.pool.Exec(ctx, suspendSQL, id.UUID(), reason, category, until)
	return err
}

func (r *repo) reinstate(ctx context.Context, id core.SellerID) error {
	_, err := r.pool.Exec(ctx, reinstateSQL, id.UUID())
	return err
}

func (r *repo) windDown(ctx context.Context, id core.SellerID, reason string) error {
	_, err := r.pool.Exec(ctx, windDownSQL, id.UUID(), reason)
	return err
}

func (r *repo) insertLifecycleEvent(ctx context.Context, sellerID core.SellerID, from, to LifecycleState, reason, category string, operatorID *uuid.UUID, payload map[string]any) error {
	payloadJSON, _ := json.Marshal(payload)
	_, err := r.pool.Exec(ctx, insertLifecycleEventSQL,
		sellerID.UUID(), string(from), string(to), reason, category, operatorID, payloadJSON,
	)
	return err
}

func (r *repo) upsertKYC(ctx context.Context, app KYCApplication) error {
	addrJSON, _ := json.Marshal(app.BusinessAddress)
	_, err := r.pool.Exec(ctx, upsertKYCSQL,
		app.SellerID.UUID(), app.LegalName, app.GSTIN, app.PAN, addrJSON,
	)
	return err
}

func (r *repo) approveKYC(ctx context.Context, id core.SellerID, reason string, by core.UserID) error {
	_, err := r.pool.Exec(ctx, approveKYCSQL, id.UUID(), reason, by.String())
	return err
}

func (r *repo) rejectKYC(ctx context.Context, id core.SellerID, reason string) error {
	_, err := r.pool.Exec(ctx, rejectKYCSQL, id.UUID(), reason)
	return err
}

func (r *repo) getKYC(ctx context.Context, id core.SellerID) (KYCApplication, error) {
	var k KYCApplication
	var sid uuid.UUID
	var addrJSON string
	err := r.pool.QueryRow(ctx, getKYCSQL, id.UUID()).
		Scan(&sid, &k.State, &k.LegalName, &k.GSTIN, &k.PAN, &addrJSON, &k.SubmittedAt, &k.DecidedAt, &k.DecisionReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return KYCApplication{}, core.ErrNotFound
		}
		return KYCApplication{}, fmt.Errorf("seller.getKYC: %w", err)
	}
	k.SellerID = core.SellerIDFromUUID(sid)
	_ = json.Unmarshal([]byte(addrJSON), &k.BusinessAddress)
	return k, nil
}
