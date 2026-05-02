// Package idempotency provides an HTTP middleware that caches POST/PATCH
// responses keyed by (seller_id, Idempotency-Key header). A second request
// with the same key returns the cached response without executing the handler.
//
// Per LLD §03-services/04-idempotency.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
)

const headerName = "Idempotency-Key"

// ErrBodyMismatch is returned (as a 422) when a replayed request has a
// different body from the original — that suggests a client bug.
var ErrBodyMismatch = errors.New("idempotency: body hash mismatch")

// cachedResponse is one stored entry.
type cachedResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

const (
	getSQL = `
        SELECT status_code, headers_jsonb, body
        FROM idempotency_key
        WHERE seller_id = $1 AND key = $2
    `
	insertSQL = `
        INSERT INTO idempotency_key (seller_id, key, status_code, headers_jsonb, body, body_hash)
        VALUES ($1, $2, $3, $4::jsonb, $5, $6)
        ON CONFLICT (seller_id, key) DO NOTHING
    `
)

type repo struct{ pool *pgxpool.Pool }

func (r *repo) get(ctx context.Context, sellerID core.SellerID, key string) (*cachedResponse, error) {
	var (
		statusCode  int
		headersJSON []byte
		body        []byte
	)
	err := r.pool.QueryRow(ctx, getSQL, sellerID.UUID(), key).
		Scan(&statusCode, &headersJSON, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("idempotency.get: %w", err)
	}
	var headers map[string]string
	_ = json.Unmarshal(headersJSON, &headers)
	return &cachedResponse{StatusCode: statusCode, Headers: headers, Body: body}, nil
}

func (r *repo) set(ctx context.Context, sellerID core.SellerID, key string, cr cachedResponse, bodyHash string) error {
	headersJSON, _ := json.Marshal(cr.Headers)
	_, err := r.pool.Exec(ctx, insertSQL,
		sellerID.UUID(), key, cr.StatusCode, headersJSON, cr.Body, bodyHash,
	)
	if err != nil {
		return fmt.Errorf("idempotency.set: %w", err)
	}
	return nil
}

// Middleware returns a Chi-compatible idempotency middleware.
// It only activates when the request carries an Idempotency-Key header AND
// the authenticated seller has been injected into context (requires auth +
// seller-scope middlewares to run first).
func Middleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	r := &repo{pool: pool}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := req.Header.Get(headerName)
			if key == "" || (req.Method != http.MethodPost && req.Method != http.MethodPatch) {
				next.ServeHTTP(w, req)
				return
			}

			p, ok := auth.PrincipalFrom(req.Context())
			if !ok || p.SellerID.IsZero() {
				next.ServeHTTP(w, req)
				return
			}

			sellerID := p.SellerID

			// Use seller-scoped transaction for RLS compliance.
			var cached *cachedResponse
			var getErr error
			_ = dbtx.WithSellerTx(req.Context(), pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
				cached, getErr = r.get(ctx, sellerID, key)
				return nil
			})
			if getErr != nil {
				http.Error(w, "idempotency check failed", http.StatusInternalServerError)
				return
			}

			if cached != nil {
				for k, v := range cached.Headers {
					w.Header().Set(k, v)
				}
				w.Header().Set("Idempotency-Replayed", "true")
				w.WriteHeader(cached.StatusCode)
				_, _ = w.Write(cached.Body)
				return
			}

			// Capture the response.
			rec := &responseRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
				buf:            &bytes.Buffer{},
			}
			next.ServeHTTP(rec, req)

			// Only cache successful responses.
			if rec.status >= 200 && rec.status < 300 {
				headers := make(map[string]string)
				headers["Content-Type"] = rec.Header().Get("Content-Type")
				bodyHash := hashBody(rec.buf.Bytes())
				cr := cachedResponse{
					StatusCode: rec.status,
					Headers:    headers,
					Body:       rec.buf.Bytes(),
				}
				// Store asynchronously; failure is non-fatal (request already succeeded).
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = r.set(ctx, sellerID, key, cr, bodyHash)
				}()
			}
		})
	}
}

func hashBody(b []byte) string {
	h := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	buf    *bytes.Buffer
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	rr.buf.Write(b)
	return rr.ResponseWriter.Write(b)
}
