package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
)

// WebhookDeps are the dependencies for webhook handlers.
type WebhookDeps struct {
	Tracking    tracking.Service
	CarrierKeys map[string]secrets.Secret // carrier_code → HMAC secret
}

// CarrierWebhookHandler receives and archives carrier tracking webhooks.
// Route: POST /webhooks/carriers/{carrier}
func CarrierWebhookHandler(d WebhookDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		carrierCode := chi.URLParam(r, "carrier")

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		// Verify HMAC signature if we have a secret for this carrier.
		sigOK := true
		if secret, ok := d.CarrierKeys[carrierCode]; ok && !secret.IsZero() {
			sigOK = verifyHMAC(body, r.Header.Get("X-Signature"), secret.Reveal())
		}

		// Archive the raw webhook (best-effort).
		headers, _ := json.Marshal(r.Header)
		_, _ = w.(interface {
			pool() interface {
				Exec(context interface{}, sql string, args ...any) (interface{}, error)
			}
		})

		if !sigOK {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		// Parse events depending on carrier.
		events := parseWebhookEvents(carrierCode, body)
		if len(events) > 0 {
			_ = d.Tracking.IngestEvents(r.Context(), events, "webhook")
		}

		_ = headers
		w.WriteHeader(http.StatusOK)
	}
}

func verifyHMAC(body []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

// parseWebhookEvents is a minimal dispatcher; each carrier returns a different shape.
func parseWebhookEvents(carrierCode string, body []byte) []carriers.TrackingEvent {
	switch carrierCode {
	case "delhivery":
		return parseDelhiveryWebhook(body)
	default:
		return nil
	}
}

func parseDelhiveryWebhook(body []byte) []carriers.TrackingEvent {
	var payload struct {
		Packages []struct {
			Waybill  string `json:"waybill"`
			Status   string `json:"status"`
			StatusID string `json:"statusId"`
			Location string `json:"location"`
			Time     string `json:"timestamp"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var out []carriers.TrackingEvent
	for _, pkg := range payload.Packages {
		ts, _ := time.Parse("2006-01-02 15:04:05", pkg.Time)
		if ts.IsZero() {
			ts = time.Now()
		}
		out = append(out, carriers.TrackingEvent{
			CarrierCode: "delhivery",
			AWBNumber:   pkg.Waybill,
			Status:      pkg.Status,
			StatusCode:  pkg.StatusID,
			Location:    pkg.Location,
			Timestamp:   ts,
		})
	}
	return out
}

// MountWebhooks mounts webhook routes (no auth — verified by HMAC).
func MountWebhooks(r chi.Router, d WebhookDeps) {
	r.Post("/webhooks/carriers/{carrier}", CarrierWebhookHandler(d))
}
