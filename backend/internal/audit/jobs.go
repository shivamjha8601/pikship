package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Verifier walks every audit chain and recomputes hashes. Returns a slice
// of failures (one per broken chain); empty if all chains are intact.
//
// Per LLD §03-services/02-audit: scheduled weekly. On any failure, fires
// the AuditChainBrokenForSeller alert (P0).
//
// pool MUST be the admin pool (BYPASSRLS) so the job can read every
// seller's chain regardless of app.seller_id.
type Verifier struct {
	repo *repo
	log  *slog.Logger
}

// NewVerifier constructs a Verifier from the admin pool.
func NewVerifier(adminPool *pgxpool.Pool, log *slog.Logger) *Verifier {
	return &Verifier{repo: newRepo(adminPool), log: log}
}

// VerifyAll iterates every seller's chain (and optionally the platform
// chain) and verifies hash integrity. Returns nil if all chains are good;
// returns an error wrapping ErrChainBroken if any chain failed.
func (v *Verifier) VerifyAll(ctx context.Context) error {
	sellers, err := v.repo.listAllSellerIDsForVerification(ctx)
	if err != nil {
		return fmt.Errorf("audit.VerifyAll: list sellers: %w", err)
	}

	var failures []string
	for _, sid := range sellers {
		if err := v.verifyOne(ctx, sid); err != nil {
			v.log.ErrorContext(ctx, "audit chain broken",
				slog.String("seller_id", sid.String()),
				slog.String("err", err.Error()),
			)
			failures = append(failures, fmt.Sprintf("seller=%s: %v", sid, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w: %d chains failed: %v", ErrChainBroken, len(failures), failures)
	}

	v.log.InfoContext(ctx, "audit chains verified",
		slog.Int("seller_count", len(sellers)),
	)
	return nil
}

func (v *Verifier) verifyOne(ctx context.Context, sellerID core.SellerID) error {
	events, hashes, err := v.repo.listSellerEventsForVerification(ctx, sellerID)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if err := VerifyChain(events, hashes); err != nil {
		// VerifyChain already wraps ErrChainBroken; pass through.
		if errors.Is(err, ErrChainBroken) {
			return err
		}
		return fmt.Errorf("audit.verifyOne: %w", err)
	}
	return nil
}
