// Command mock-delhivery is a tiny in-memory Delhivery API mock for local
// development. It mimics the subset of the Delhivery One-Click / Express API
// that internal/carriers/delhivery hits — enough to exercise booking,
// cancellation, tracking, and label fetch without holding a real API key.
//
// Endpoints (matching Delhivery's public docs):
//
//	GET  /c/api/pin-codes/json/?filter_codes=<pincode>
//	POST /api/cmu/create.json            (form-encoded: format=json&data=<JSON>)
//	POST /api/p/edit                     (form-encoded cancel: data={"waybill":"...","cancellation":"true"})
//	GET  /api/v1/packages/json/?waybill=<awb>&verbose=2
//	GET  /api/p/packing_slip?wbns=<awb>&pdf=true
//	GET  /                               human-readable index
//
// Run:
//
//	go run . -addr :8088
//
// Then point the adapter at it:
//
//	PIKSHIPP_DELHIVERY_BASE_URL=http://localhost:8088
//
// The store lives in process memory, so restarting wipes all shipments.
// This is throwaway scaffolding; don't ship it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", ":8088", "listen address")
	flag.Parse()

	srv := newServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.index)
	mux.HandleFunc("/c/api/pin-codes/json/", srv.pincodeServiceability)
	mux.HandleFunc("/api/cmu/create.json", srv.createShipment)
	mux.HandleFunc("/api/p/edit", srv.editShipment)
	mux.HandleFunc("/api/v1/packages/json/", srv.trackShipment)
	mux.HandleFunc("/api/p/packing_slip", srv.packingSlip)

	log.Printf("mock-delhivery listening on %s", *addr)
	if err := http.ListenAndServe(*addr, logRequest(mux)); err != nil {
		log.Fatal(err)
	}
}

type shipment struct {
	Waybill        string
	OrderRef       string
	ConsigneeName  string
	ConsigneePhone string
	ShipToPincode  string
	WeightG        int
	PaymentMode    string // Prepaid / COD
	CODAmount      float64
	Status         string // Manifested / In Transit / Delivered / Cancelled
	CreatedAt      time.Time
	LastUpdated    time.Time
	Cancelled      bool
}

type server struct {
	mu        sync.RWMutex
	shipments map[string]*shipment
	rng       *rand.Rand
}

func newServer() *server {
	return &server{
		shipments: make(map[string]*shipment),
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ─── Endpoints ────────────────────────────────────────────────────────────

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><body style="font-family:system-ui;padding:2rem;max-width:60rem;margin:auto">
<h1>mock-delhivery</h1>
<p>In-memory mock of the Delhivery Express API for local dev.</p>
<h2>Endpoints</h2>
<ul>
  <li><code>GET  /c/api/pin-codes/json/?filter_codes=560001</code> — serviceability</li>
  <li><code>POST /api/cmu/create.json</code> — create shipment (form: <code>format=json&amp;data=&lt;json&gt;</code>)</li>
  <li><code>POST /api/p/edit</code> — cancel shipment</li>
  <li><code>GET  /api/v1/packages/json/?waybill=&lt;awb&gt;&amp;verbose=2</code> — track</li>
  <li><code>GET  /api/p/packing_slip?wbns=&lt;awb&gt;&amp;pdf=true</code> — label stub</li>
</ul>
<p>Status auto-progresses with shipment age: Manifested &rarr; In Transit (30s) &rarr; Out for Delivery (60s) &rarr; Delivered (90s).</p>
</body></html>`)
}

var pincodeRX = regexp.MustCompile(`^[1-9][0-9]{5}$`)

func (s *server) pincodeServiceability(w http.ResponseWriter, r *http.Request) {
	pin := r.URL.Query().Get("filter_codes")
	if !pincodeRX.MatchString(pin) {
		writeJSON(w, http.StatusOK, map[string]any{
			"delivery_codes": []any{},
		})
		return
	}
	// Real Delhivery response shape; we mimic the relevant fields.
	writeJSON(w, http.StatusOK, map[string]any{
		"delivery_codes": []any{
			map[string]any{
				"postal_code": map[string]any{
					"pin":            pin,
					"city":           cityForPin(pin),
					"state_code":     stateForPin(pin),
					"district":       cityForPin(pin),
					"pre_paid":       "Y",
					"cash":           "Y",
					"cod":            "Y",
					"pickup":         "Y",
					"max_amount":     50000,
					"is_oda":         "N",
					"covid_zone":     "Green",
					"sort_code":      "BLR",
					"country_code":   "IN",
					"remarks":        "",
					"protect_blacklist": "N",
				},
			},
		},
	})
}

// Delhivery's create-shipment payload comes in form-encoded as
//
//	format=json
//	data={"shipments":[ ... ], "pickup_location":{ ... }}
//
// We unmarshal the data field and respond with one waybill per input.
func (s *server) createShipment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("parse form: "+err.Error()))
		return
	}
	raw := r.FormValue("data")
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, errResp("missing data"))
		return
	}
	var req struct {
		Shipments []struct {
			Name        string  `json:"name"`
			Add         string  `json:"add"`
			Pin         string  `json:"pin"`
			City        string  `json:"city"`
			State       string  `json:"state"`
			Country     string  `json:"country"`
			Phone       string  `json:"phone"`
			OrderID     string  `json:"order"`
			PaymentMode string  `json:"payment_mode"`
			Weight      float64 `json:"weight"`
			TotalAmount float64 `json:"total_amount"`
			CODAmount   float64 `json:"cod_amount"`
		} `json:"shipments"`
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid json: "+err.Error()))
		return
	}
	if len(req.Shipments) == 0 {
		writeJSON(w, http.StatusBadRequest, errResp("no shipments in payload"))
		return
	}

	now := time.Now()
	packages := make([]map[string]any, 0, len(req.Shipments))
	s.mu.Lock()
	for _, in := range req.Shipments {
		awb := s.newWaybill()
		// Weight is sent in kilograms as a float; convert to grams for storage.
		weightG := int(in.Weight * 1000)
		s.shipments[awb] = &shipment{
			Waybill:        awb,
			OrderRef:       in.OrderID,
			ConsigneeName:  in.Name,
			ConsigneePhone: in.Phone,
			ShipToPincode:  in.Pin,
			WeightG:        weightG,
			PaymentMode:    in.PaymentMode,
			CODAmount:      in.CODAmount,
			Status:         "Manifested",
			CreatedAt:      now,
			LastUpdated:    now,
		}
		packages = append(packages, map[string]any{
			"waybill":         awb,
			"refnum":          in.OrderID,
			"status":          "Success",
			"remarks":         []any{},
			"sort_code":       "BLR",
			"client":          "PIKSHIPP-MOCK",
			"cod_amount":      in.CODAmount,
			"payment":         in.PaymentMode,
		})
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"package_count": len(packages),
		"packages":      packages,
		// Real Delhivery returns an array here; the adapter decodes it as one.
		"upload_wbn": []string{fmt.Sprintf("UPLOAD-%d", now.UnixMilli())},
	})
}

func (s *server) editShipment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("parse form: "+err.Error()))
		return
	}
	raw := r.FormValue("data")
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, errResp("missing data"))
		return
	}
	var req struct {
		Waybill      string `json:"waybill"`
		Cancellation string `json:"cancellation"`
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid json: "+err.Error()))
		return
	}
	s.mu.Lock()
	ship, ok := s.shipments[req.Waybill]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  false,
			"error":   "unknown waybill",
			"waybill": req.Waybill,
		})
		return
	}
	if strings.EqualFold(req.Cancellation, "true") {
		ship.Cancelled = true
		ship.Status = "Cancelled"
		ship.LastUpdated = time.Now()
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  true,
		"waybill": req.Waybill,
		"remark":  "Cancelled successfully",
	})
}

func (s *server) trackShipment(w http.ResponseWriter, r *http.Request) {
	awb := r.URL.Query().Get("waybill")
	if awb == "" {
		writeJSON(w, http.StatusBadRequest, errResp("missing waybill"))
		return
	}
	s.mu.RLock()
	ship, ok := s.shipments[awb]
	s.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"ShipmentData": []any{},
		})
		return
	}
	status := autoStatus(ship)
	scans := buildScans(ship, status)

	// The internal Delhivery adapter expects Shipment.Status to be a string
	// (current status text), not the nested object the real-world API returns.
	// Emit a string so json.Unmarshal succeeds and the scans get parsed.
	writeJSON(w, http.StatusOK, map[string]any{
		"ShipmentData": []any{
			map[string]any{
				"Shipment": map[string]any{
					"AWB":             ship.Waybill,
					"ReferenceNo":     ship.OrderRef,
					"Status":          status,
					"StatusDateTime":  ship.LastUpdated.Format("2006-01-02T15:04:05"),
					"StatusCode":      statusCode(status),
					"Origin":          "Bangalore_HUB",
					"Destination":     cityForPin(ship.ShipToPincode),
					"DestRecieveBy":   "",
					"Scans":           scans,
					"CODAmount":       ship.CODAmount,
					"OrderType":       ship.PaymentMode,
				},
			},
		},
	})
}

func (s *server) packingSlip(w http.ResponseWriter, r *http.Request) {
	awb := r.URL.Query().Get("wbns")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=label-%s.pdf", awb))
	// Tiny PDF stub. Real PDF would be useful for adapters that parse labels,
	// but no consumer does that today.
	fmt.Fprintf(w, "%%PDF-1.4\n%% mock-delhivery label for %s\n%%%%EOF\n", awb)
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func (s *server) newWaybill() string {
	// Delhivery waybills are 12-digit numerics in real life.
	return fmt.Sprintf("MOCK%010d", s.rng.Int63n(1e10))
}

func autoStatus(sh *shipment) string {
	if sh.Cancelled {
		return "Cancelled"
	}
	age := time.Since(sh.CreatedAt)
	switch {
	case age < 30*time.Second:
		return "Manifested"
	case age < 60*time.Second:
		return "In Transit"
	case age < 90*time.Second:
		return "Out for Delivery"
	default:
		return "Delivered"
	}
}

func statusCode(s string) string {
	switch s {
	case "Manifested":
		return "MAN"
	case "In Transit":
		return "TIN"
	case "Out for Delivery":
		return "OFD"
	case "Delivered":
		return "DLV"
	case "Cancelled":
		return "CAN"
	}
	return ""
}

// The internal Delhivery adapter parses scans as a flat list with
// "2006-01-02T15:04:05" timestamps — no nested ScanDetail, no timezone.
// We emit that shape so the adapter actually deserialises into events.
func buildScans(sh *shipment, current string) []any {
	stages := []struct {
		label string
		at    time.Duration
	}{
		{"Manifested", 0},
		{"In Transit", 30 * time.Second},
		{"Out for Delivery", 60 * time.Second},
		{"Delivered", 90 * time.Second},
	}
	const tsLayout = "2006-01-02T15:04:05"
	scans := make([]any, 0, len(stages))
	for _, st := range stages {
		if st.label == "Manifested" || time.Since(sh.CreatedAt) >= st.at {
			scans = append(scans, map[string]any{
				"ScanDateTime":    sh.CreatedAt.Add(st.at).Format(tsLayout),
				"Scan":            st.label,
				"ScanType":        statusCode(st.label),
				"Instructions":    "",
				"ScannedLocation": "Bangalore_HUB",
				"StatusCode":      statusCode(st.label),
			})
		}
		if st.label == current {
			break
		}
	}
	return scans
}

var (
	pinToCity = map[byte]string{
		'1': "Delhi", '2': "Lucknow", '3': "Ahmedabad", '4': "Mumbai",
		'5': "Pune", '6': "Bangalore", '7': "Bhubaneswar",
		'8': "Guwahati", '9': "Hyderabad",
	}
	pinToState = map[byte]string{
		'1': "DL", '2': "UP", '3': "GJ", '4': "MH",
		'5': "MH", '6': "KA", '7': "OD",
		'8': "AS", '9': "TG",
	}
)

func cityForPin(pin string) string {
	if pin == "" {
		return ""
	}
	if c, ok := pinToCity[pin[0]]; ok {
		return c
	}
	return "Unknown"
}

func stateForPin(pin string) string {
	if pin == "" {
		return ""
	}
	if s, ok := pinToState[pin[0]]; ok {
		return s
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errResp(msg string) map[string]any {
	return map[string]any{"success": false, "error": msg}
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		q := ""
		if r.URL.RawQuery != "" {
			q = "?" + r.URL.RawQuery
		}
		log.Printf("%-4s %s%s  %s", r.Method, r.URL.Path, q, time.Since(start))
	})
}
