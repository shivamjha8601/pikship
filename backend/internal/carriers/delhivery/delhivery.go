// Package delhivery implements the carrier.Adapter for Delhivery.
//
// API base: https://track.delhivery.com (prod) / https://staging-express.delhivery.com (staging)
// Auth: Token <api_key> header.
//
// Per LLD §04-adapters/01-delhivery.
package delhivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

const (
	prodBaseURL    = "https://track.delhivery.com"
	stagingBaseURL = "https://staging-express.delhivery.com"
)

// Config holds Delhivery adapter configuration.
type Config struct {
	APIKey     secrets.Secret
	ClientName string
	Staging    bool
}

// Adapter is the Delhivery carrier adapter.
type Adapter struct {
	cfg      Config
	baseURL  string
	httpClient *http.Client
}

// New creates a new Delhivery adapter.
func New(cfg Config) *Adapter {
	base := prodBaseURL
	if cfg.Staging {
		base = stagingBaseURL
	}
	return &Adapter{
		cfg:        cfg,
		baseURL:    base,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Adapter) Code() string        { return "delhivery" }
func (a *Adapter) DisplayName() string { return "Delhivery" }
func (a *Adapter) Capabilities() carriers.Capabilities {
	return carriers.Capabilities{
		Services:              []core.ServiceType{core.ServiceTypeStandard, core.ServiceTypeExpress},
		SupportsCOD:           true,
		MaxDeclaredValuePaise: core.FromRupees(5_000_000),
		MaxWeightG:            50_000,
		MaxLengthMM:           1500,
		SupportsNDRActions:    true,
		SupportsLabelFetch:    true,
	}
}

func (a *Adapter) CheckServiceability(ctx context.Context, q carriers.ServiceabilityQuery) (bool, error) {
	endpoint := fmt.Sprintf("%s/c/api/pin-codes/json/?filter_codes=%s", a.baseURL, url.QueryEscape(string(q.ShipToPincode)))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Token "+a.cfg.APIKey.Reveal())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("delhivery.CheckServiceability: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		DeliveryServiceAvailable bool `json:"delivery_codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, nil
	}
	return body.DeliveryServiceAvailable, nil
}

func (a *Adapter) Book(ctx context.Context, req carriers.BookRequest) carriers.Result[carriers.BookResponse] {
	// Delhivery uses a form-encoded shipment creation payload.
	payload := map[string]any{
		"shipments": []map[string]any{{
			"name":              req.ReceiverContact.Name,
			"add":               addressLine(req.ShippingAddress),
			"pin":               string(req.ShipToPincode),
			"city":              req.ShippingAddress.City,
			"state":             req.ShippingAddress.State,
			"country":           "India",
			"phone":             req.ReceiverContact.Phone,
			"order":             req.ShipmentID.String(),
			"payment_mode":      paymentModeStr(req.PaymentMode),
			"return_pin":        string(req.PickupPincode),
			"return_city":       req.PickupAddress.City,
			"return_phone":      req.PickupContact.Phone,
			"return_add":        addressLine(req.PickupAddress),
			"return_state":      req.PickupAddress.State,
			"return_country":    "India",
			"return_name":       req.PickupContact.Name,
			"products_desc":     req.InvoiceNumber,
			"hsn_code":          "",
			"cod_amount":        int64(req.CODAmount),
			"order_date":        time.Now().Format("2006-01-02T15:04:05"),
			"total_amount":      int64(req.DeclaredValue),
			"seller_add":        addressLine(req.PickupAddress),
			"seller_name":       req.PickupContact.Name,
			"seller_inv":        req.InvoiceNumber,
			"quantity":          1,
			"weight":            float64(req.DeclaredWeightG) / 1000,
			"volumetric_weight": float64(req.LengthMM*req.WidthMM*req.HeightMM) / 5_000_000.0,
		}},
		"pickup_location": map[string]any{"name": a.cfg.ClientName},
	}

	body, _ := json.Marshal(payload)
	form := url.Values{"format": {"json"}, "data": {string(body)}}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/api/cmu/create.json", strings.NewReader(form.Encode()))
	if err != nil {
		return carriers.Result[carriers.BookResponse]{Err: fmt.Errorf("delhivery.Book: %w", err), ErrClass: carriers.ErrClassTransient}
	}
	httpReq.Header.Set("Authorization", "Token "+a.cfg.APIKey.Reveal())
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return carriers.Result[carriers.BookResponse]{Err: fmt.Errorf("delhivery.Book: http: %w", err), ErrClass: carriers.ErrClassTransient}
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return carriers.Result[carriers.BookResponse]{
			Err:      fmt.Errorf("delhivery.Book: status %d: %s", resp.StatusCode, rawBody),
			ErrClass: errClassFromStatus(resp.StatusCode),
			RawBody:  rawBody,
		}
	}

	var result struct {
		Packages []struct {
			Waybill string `json:"waybill"`
			Status  string `json:"status"`
			Error   string `json:"error"`
		} `json:"packages"`
		UploadWaybill []string `json:"upload_wbn"`
	}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return carriers.Result[carriers.BookResponse]{Err: fmt.Errorf("delhivery.Book: decode: %w", err), ErrClass: carriers.ErrClassTransient, RawBody: rawBody}
	}
	if len(result.Packages) == 0 {
		return carriers.Result[carriers.BookResponse]{Err: fmt.Errorf("delhivery.Book: no packages in response"), ErrClass: carriers.ErrClassTransient, RawBody: rawBody}
	}
	pkg := result.Packages[0]
	if pkg.Error != "" {
		return carriers.Result[carriers.BookResponse]{Err: fmt.Errorf("delhivery.Book: %s", pkg.Error), ErrClass: carriers.ErrClassPermanent, RawBody: rawBody}
	}

	return carriers.Result[carriers.BookResponse]{
		Value:   carriers.BookResponse{AWBNumber: pkg.Waybill, CarrierShipmentRef: pkg.Waybill},
		RawBody: rawBody,
	}
}

func (a *Adapter) Cancel(ctx context.Context, req carriers.CancelRequest) carriers.Result[carriers.CancelResponse] {
	form := url.Values{"waybill": {req.AWBNumber}, "cancellation": {"true"}}
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/api/p/edit", bytes.NewBufferString(form.Encode()))
	httpReq.Header.Set("Authorization", "Token "+a.cfg.APIKey.Reveal())
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return carriers.Result[carriers.CancelResponse]{Err: fmt.Errorf("delhivery.Cancel: %w", err), ErrClass: carriers.ErrClassTransient}
	}
	defer resp.Body.Close()
	return carriers.Result[carriers.CancelResponse]{Value: carriers.CancelResponse{Cancelled: resp.StatusCode == http.StatusOK}}
}

func (a *Adapter) FetchLabel(ctx context.Context, req carriers.LabelRequest) carriers.Result[carriers.LabelResponse] {
	endpoint := fmt.Sprintf("%s/api/p/packing_slip?wbns=%s&pdf=true", a.baseURL, url.QueryEscape(req.AWBNumber))
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	httpReq.Header.Set("Authorization", "Token "+a.cfg.APIKey.Reveal())

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return carriers.Result[carriers.LabelResponse]{Err: fmt.Errorf("delhivery.FetchLabel: %w", err), ErrClass: carriers.ErrClassTransient}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return carriers.Result[carriers.LabelResponse]{Value: carriers.LabelResponse{Format: "pdf", Data: data}}
}

func (a *Adapter) FetchTrackingEvents(ctx context.Context, awb string, since time.Time) carriers.Result[[]carriers.TrackingEvent] {
	endpoint := fmt.Sprintf("%s/api/v1/packages/json/?waybill=%s&verbose=2", a.baseURL, url.QueryEscape(awb))
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	httpReq.Header.Set("Authorization", "Token "+a.cfg.APIKey.Reveal())

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return carriers.Result[[]carriers.TrackingEvent]{Err: fmt.Errorf("delhivery.FetchTracking: %w", err), ErrClass: carriers.ErrClassTransient}
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)

	var result struct {
		ShipmentData []struct {
			Shipment struct {
				AWB     string `json:"waybill"`
				Scans   []struct {
					ScanDateTime string `json:"ScanDateTime"`
					ScanType     string `json:"ScanType"`
					Scan         string `json:"Scan"`
					ScannedLoc   string `json:"ScannedLocation"`
				} `json:"Scans"`
				Status string `json:"Status"`
			} `json:"Shipment"`
		} `json:"ShipmentData"`
	}
	if err := json.Unmarshal(rawBody, &result); err != nil || len(result.ShipmentData) == 0 {
		return carriers.Result[[]carriers.TrackingEvent]{Value: nil}
	}

	var events []carriers.TrackingEvent
	for _, s := range result.ShipmentData[0].Shipment.Scans {
		t, _ := time.Parse("2006-01-02T15:04:05", s.ScanDateTime)
		if t.Before(since) {
			continue
		}
		events = append(events, carriers.TrackingEvent{
			AWBNumber:   awb,
			Status:      s.Scan,
			StatusCode:  s.ScanType,
			Location:    s.ScannedLoc,
			Timestamp:   t,
			IsDelivered: s.ScanType == "DL",
			IsRTO:       s.ScanType == "RTO",
		})
	}
	return carriers.Result[[]carriers.TrackingEvent]{Value: events, RawBody: rawBody}
}

func (a *Adapter) RaiseNDRAction(_ context.Context, req carriers.NDRActionRequest) carriers.Result[carriers.NDRActionResponse] {
	return carriers.Result[carriers.NDRActionResponse]{
		Value:    carriers.NDRActionResponse{Accepted: false, Message: "delhivery: NDR action API not yet wired"},
		ErrClass: carriers.ErrClassUnsupported,
	}
}

func addressLine(addr core.Address) string {
	parts := []string{addr.Line1}
	if addr.Line2 != "" {
		parts = append(parts, addr.Line2)
	}
	return strings.Join(parts, ", ")
}

func paymentModeStr(pm core.PaymentMode) string {
	if pm == core.PaymentModeCOD {
		return "COD"
	}
	return "Pre-paid"
}

func errClassFromStatus(status int) carriers.ErrorClass {
	switch {
	case status == 401 || status == 403:
		return carriers.ErrClassAuth
	case status >= 500:
		return carriers.ErrClassTransient
	default:
		return carriers.ErrClassPermanent
	}
}
