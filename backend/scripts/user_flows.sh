#!/usr/bin/env bash
# user_flows.sh — Demonstrates Pikshipp's core user flows via HTTP API.
#
# Prerequisites:
#   1. The binary is running: make run  (listens on :8081 by default)
#   2. jq is installed: brew install jq
#
# Usage:
#   ./scripts/user_flows.sh
#
# Each section is self-contained. The script seeds data, exercises
# endpoints, and logs what each call actually does.

set -euo pipefail

BASE="${PIKSHIPP_URL:-http://localhost:8081}"
V1="$BASE/v1"

# Colors for readability.
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

step()  { echo -e "\n${CYAN}▶ $*${NC}"; }
ok()    { echo -e "${GREEN}  ✓ $*${NC}"; }
info()  { echo -e "${YELLOW}  → $*${NC}"; }
fail()  { echo -e "${RED}  ✗ $*${NC}"; exit 1; }

call() {
    # call <METHOD> <PATH> [body]
    local method=$1 path=$2 body=${3:-}
    if [[ -n "$body" ]]; then
        curl -s -X "$method" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${TOKEN:-}" \
            -d "$body" \
            "$V1$path"
    else
        curl -s -X "$method" \
            -H "Authorization: Bearer ${TOKEN:-}" \
            "$V1$path"
    fi
}

# ════════════════════════════════════════════════════════════════════════════
# Flow 0: Health check — verify the server is running
# ════════════════════════════════════════════════════════════════════════════
step "Flow 0: Health check"
info "GET $BASE/healthz — process alive check (no DB ping)"
HEALTH=$(curl -sf "$BASE/healthz") || fail "Server not running at $BASE"
echo "  Response: $HEALTH"
ok "Server is alive"

info "GET $BASE/readyz — DB connectivity check"
READY=$(curl -sf "$BASE/readyz") || fail "Server not ready (DB down?)"
echo "  Response: $READY"
ok "Server is ready (DB pool OK)"

# ════════════════════════════════════════════════════════════════════════════
# Flow 1: Seller onboarding — create account, select seller, submit KYC
# ════════════════════════════════════════════════════════════════════════════
step "Flow 1: Seller onboarding"

# NOTE: In production this goes through Google OAuth. For the demo we
# assume a session token was already issued by the auth service.
# Simulate by reading PIKSHIPP_TEST_TOKEN from environment; if absent
# the script will show the request shapes but skip live assertions.

if [[ -z "${PIKSHIPP_TEST_TOKEN:-}" ]]; then
    echo ""
    info "PIKSHIPP_TEST_TOKEN not set — showing request shapes only."
    info "To run live: set PIKSHIPP_TEST_TOKEN=<your session token>"
    echo ""

    info "POST /v1/auth/select-seller"
    info "  Body: {\"seller_id\": \"<uuid>\"}"
    info "  → Returns new session token scoped to that seller."
    info "  → Sets the seller context for all subsequent RLS-scoped queries."
    echo ""

    info "GET /v1/me"
    info "  → Returns user profile + list of all seller memberships."
    echo ""

    info "GET /v1/seller"
    info "  → Returns the currently-selected seller's profile."
    echo ""

    info "POST /v1/seller/kyc"
    info "  Body: {\"legal_name\":\"ACME Ltd\",\"gstin\":\"29AABCU9603R1ZX\",\"pan\":\"AABCU9603R\"}"
    info "  → Submits KYC for operator review. Transitions seller from provisioning → sandbox."
else
    export TOKEN="$PIKSHIPP_TEST_TOKEN"

    step "  GET /v1/me — who am I?"
    ME=$(call GET "/me")
    echo "  $(echo "$ME" | jq -r '"User: \(.user.email // "n/a") | Sellers: \(.sellers | length)"')"
    ok "Got user profile"

    # Select first available seller.
    SELLER_ID=$(echo "$ME" | jq -r '.sellers[0].seller_id // empty')
    if [[ -z "$SELLER_ID" ]]; then
        fail "No seller memberships found — run the onboarding flow first"
    fi

    step "  POST /v1/auth/select-seller — scope session to seller $SELLER_ID"
    info "This re-issues the session token so all subsequent queries are RLS-scoped."
    SELECT=$(call POST "/auth/select-seller" "{\"seller_id\":\"$SELLER_ID\"}")
    TOKEN=$(echo "$SELECT" | jq -r '.token')
    export TOKEN
    ok "Session scoped to seller $SELLER_ID"

    step "  GET /v1/seller — seller profile"
    SELLER=$(call GET "/seller")
    echo "  $(echo "$SELLER" | jq -r '"Name: \(.display_name) | State: \(.lifecycle_state) | Type: \(.seller_type)"')"
    ok "Got seller profile"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 2: Catalog setup — pickup location + product
# ════════════════════════════════════════════════════════════════════════════
step "Flow 2: Catalog setup"

if [[ -z "${PIKSHIPP_TEST_TOKEN:-}" ]]; then
    info "POST /v1/pickup-locations"
    cat <<'JSON'
  Body: {
    "label": "Main Warehouse",
    "contact_name": "Ravi Kumar",
    "contact_phone": "+919876543210",
    "address": {
      "line1": "Plot 12, Andheri East",
      "city": "Mumbai",
      "state": "Maharashtra",
      "country": "IN",
      "pincode": "400093"
    },
    "pincode": "400093",
    "state": "Maharashtra",
    "active": true,
    "is_default": true
  }
JSON
    info "→ Creates a pickup location (warehouse) where carrier will collect shipments."
    info "→ Unique per (seller_id, label). First one marked is_default=true auto-selected."

    echo ""
    info "PUT /v1/products"
    cat <<'JSON'
  Body: {
    "sku": "TSHIRT-L-RED",
    "name": "Red T-Shirt Large",
    "unit_weight_g": 300,
    "length_mm": 280, "width_mm": 200, "height_mm": 30,
    "unit_price_paise": 79900
  }
JSON
    info "→ Upsert by SKU. Weight + dims used to calculate volumetric weight for pricing."
else
    step "  POST /v1/pickup-locations — create a warehouse"
    PICKUP=$(call POST "/pickup-locations" '{
        "label":"Main Warehouse",
        "contact_name":"Ravi Kumar",
        "contact_phone":"+919876543210",
        "address":{"line1":"Plot 12, Andheri East","city":"Mumbai","state":"Maharashtra","country":"IN","pincode":"400093"},
        "pincode":"400093","state":"Maharashtra","active":true,"is_default":true
    }')
    PICKUP_ID=$(echo "$PICKUP" | jq -r '.id // empty')
    if [[ -z "$PICKUP_ID" ]]; then
        info "Pickup location may already exist — listing instead"
        PICKUP_ID=$(call GET "/pickup-locations" | jq -r '.[0].id')
    fi
    ok "Pickup location: $PICKUP_ID"

    step "  PUT /v1/products — upsert product by SKU"
    call PUT "/products" '{
        "sku":"TSHIRT-L-RED","name":"Red T-Shirt Large",
        "unit_weight_g":300,"length_mm":280,"width_mm":200,"height_mm":30,
        "unit_price_paise":79900,"active":true
    }' > /dev/null
    ok "Product upserted"

    step "  GET /v1/products — list catalogue"
    PRODUCTS=$(call GET "/products")
    echo "  $(echo "$PRODUCTS" | jq -r 'length | "Total products: \(.)"')"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 3: Order creation + ready flow
# ════════════════════════════════════════════════════════════════════════════
step "Flow 3: Create and ready an order"

if [[ -z "${PIKSHIPP_TEST_TOKEN:-}" ]]; then
    info "POST /v1/orders"
    cat <<'JSON'
  Body: {
    "channel": "shopify",
    "channel_order_id": "SHOP-1001",
    "buyer_name": "Priya Patel",
    "buyer_phone": "+919123456789",
    "billing_address": { ... },
    "shipping_address": { "pincode": "560001", "city": "Bengaluru", ... },
    "payment_method": "prepaid",
    "total_paise": 79900,
    "pickup_location_id": "<uuid>",
    "package_weight_g": 300,
    "lines": [{ "sku": "TSHIRT-L-RED", "quantity": 1, "unit_price_paise": 79900 }]
  }
JSON
    info "→ Creates order in 'draft' state."
    info "→ channel + channel_order_id must be unique per seller (idempotency key)."

    echo ""
    info "POST /v1/orders/{orderID}/cancel"
    info "  Body: {\"reason\": \"customer request\"}"
    info "  → Cancels order. Allowed from draft, ready, allocating, booked (before pickup)."
else
    step "  POST /v1/orders — create new order"
    PICKUP_ID_FOR_ORDER="${PICKUP_ID:-$(call GET "/pickup-locations" | jq -r '.[0].id')}"

    ORDER=$(call POST "/orders" "{
        \"channel\":\"shopify\",
        \"channel_order_id\":\"DEMO-$(date +%s)\",
        \"buyer_name\":\"Priya Patel\",
        \"buyer_phone\":\"+919123456789\",
        \"billing_address\":{\"line1\":\"5 MG Road\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"country\":\"IN\",\"pincode\":\"560001\"},
        \"shipping_address\":{\"line1\":\"5 MG Road\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"country\":\"IN\",\"pincode\":\"560001\"},
        \"shipping_pincode\":\"560001\",
        \"shipping_state\":\"Karnataka\",
        \"payment_method\":\"prepaid\",
        \"subtotal_paise\":79900,
        \"total_paise\":79900,
        \"pickup_location_id\":\"$PICKUP_ID_FOR_ORDER\",
        \"package_weight_g\":300,
        \"package_length_mm\":280,
        \"package_width_mm\":200,
        \"package_height_mm\":30,
        \"lines\":[{\"sku\":\"TSHIRT-L-RED\",\"name\":\"Red T-Shirt Large\",\"quantity\":1,\"unit_price_paise\":79900,\"unit_weight_g\":300}]
    }")
    ORDER_ID=$(echo "$ORDER" | jq -r '.id')
    ORDER_STATE=$(echo "$ORDER" | jq -r '.state')
    echo "  Order $ORDER_ID — state=$ORDER_STATE"
    ok "Order created in draft"

    step "  GET /v1/orders — list all orders"
    ORDER_COUNT=$(call GET "/orders" | jq -r '.orders | length')
    ok "$ORDER_COUNT order(s) in seller account"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 4: Wallet — check balance
# ════════════════════════════════════════════════════════════════════════════
step "Flow 4: Wallet balance check"

info "GET /v1/wallet/balance"
info "  → Returns balance, hold_total, available, credit_limit."
info "  → available = balance + credit_limit - hold_total."
info "  → All amounts in paise (1 INR = 100 paise)."

if [[ -n "${PIKSHIPP_TEST_TOKEN:-}" ]]; then
    BAL=$(call GET "/wallet/balance")
    echo "  Balance:     $(echo "$BAL" | jq -r '.balance') paise"
    echo "  Hold total:  $(echo "$BAL" | jq -r '.hold_total') paise"
    echo "  Available:   $(echo "$BAL" | jq -r '.available') paise"
    ok "Wallet balance retrieved"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 5: Shipment tracking (after booking)
# ════════════════════════════════════════════════════════════════════════════
step "Flow 5: Shipment tracking"

info "GET /v1/shipments/{shipmentID}/tracking"
info "  → Returns all tracking events (webhook + poll) sorted newest first."
info "  Canonical statuses: picked_up | in_transit | out_for_delivery | delivered | rto_initiated | rto_delivered | exception"

echo ""
info "GET /track/{token} (no auth — buyer-facing)"
info "  → Token is issued by POST /v1/buyers/tracking-tokens (requires seller auth)."
info "  → Returns shipment state, carrier name, and event history."
info "  → Token has configurable TTL; expired tokens return 404."

echo ""
info "GET /track/awb/{awb} (no auth)"
info "  → Direct lookup by AWB for buyer self-service."

if [[ -n "${PIKSHIPP_TEST_TOKEN:-}" && -n "${ORDER_ID:-}" ]]; then
    # Try to find shipment for our demo order.
    SHIPMENT_LIST=$(call GET "/orders/$ORDER_ID" 2>/dev/null || echo "{}")
    info "Order $ORDER_ID is in state: $(echo "$SHIPMENT_LIST" | jq -r '.state // "unknown"')"
    info "(Shipment booking requires allocation engine + rate cards; demo shows structure only)"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 6: Reports dashboard
# ════════════════════════════════════════════════════════════════════════════
step "Flow 6: Reports — shipment summary"

info "GET /v1/reports/shipments/summary?from=2024-01-01&to=2024-12-31"
info "  → Aggregate counts: total, delivered, RTO, pending."
info "  → Revenue: total charges in paise, COD collected in paise."
info "  → Use ?from=YYYY-MM-DD&to=YYYY-MM-DD to control date range."

if [[ -n "${PIKSHIPP_TEST_TOKEN:-}" ]]; then
    FROM=$(date -v-30d +%Y-%m-%d 2>/dev/null || date -d "30 days ago" +%Y-%m-%d)
    TO=$(date +%Y-%m-%d)
    SUMMARY=$(call GET "/reports/shipments/summary?from=$FROM&to=$TO")
    echo "  Last 30 days:"
    echo "  Shipments:  $(echo "$SUMMARY" | jq -r '.total_shipments // 0')"
    echo "  Delivered:  $(echo "$SUMMARY" | jq -r '.delivered // 0')"
    echo "  RTO:        $(echo "$SUMMARY" | jq -r '.rto // 0')"
    echo "  Charges:    $(echo "$SUMMARY" | jq -r '.revenue_charges_paise // 0') paise"
    ok "Report generated"
fi

# ════════════════════════════════════════════════════════════════════════════
# Flow 7: NDR — handle failed delivery attempt
# ════════════════════════════════════════════════════════════════════════════
step "Flow 7: NDR — handle failed delivery attempt"

info "GET /v1/shipments/{shipmentID}/ndr"
info "  → Returns the active NDR case if the carrier attempted delivery and failed."

echo ""
info "POST /v1/ndr/{caseID}/action"
cat <<'JSON'
  Body (reattempt): { "action": "reattempt" }
  Body (address):   { "action": "change_address", "new_address": { "line1": "...", "pincode": "..." } }
  Body (rto):       { "action": "rto" }
JSON
info "  → Sends instruction to carrier via adapter.RaiseNDRAction."
info "  → Allowed within 48h response window before auto-RTO sweep triggers."

# ════════════════════════════════════════════════════════════════════════════
# Flow 8: Webhook simulation — carrier pushes a tracking event
# ════════════════════════════════════════════════════════════════════════════
step "Flow 8: Carrier webhook (no auth, HMAC-verified)"

info "POST /webhooks/carriers/delhivery"
info "  Header: X-Signature: <hmac-sha256 of body>"
cat <<'JSON'
  Body (Delhivery format): {
    "packages": [{
      "waybill": "DEL12345",
      "status": "Delivered",
      "statusId": "DL",
      "location": "Bengaluru Hub"
    }]
  }
JSON
info "  → Archives raw payload to carrier_webhook_archive."
info "  → Normalises status to canonical (DL → delivered)."
info "  → Drives shipment FSM: booked→in_transit or in_transit→delivered."
info "  → Idempotent via dedupe_key = sha256(carrier|awb|statusCode|timestamp)."

# ════════════════════════════════════════════════════════════════════════════
# Demo webhook call (always live, no auth required)
# ════════════════════════════════════════════════════════════════════════════
WEBHOOK_RESP=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "$BASE/webhooks/carriers/delhivery" \
    -H "Content-Type: application/json" \
    -d '{"packages":[{"waybill":"DEMO_AWB_123","status":"Delivered","statusId":"DL","location":"Mumbai"}]}')
if [[ "$WEBHOOK_RESP" == "200" ]]; then
    ok "Webhook endpoint alive (200 — unknown AWB silently ignored)"
else
    info "Webhook returned HTTP $WEBHOOK_RESP (expected 200)"
fi

# ════════════════════════════════════════════════════════════════════════════
echo -e "\n${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  All user flow demonstrations complete.${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo ""
echo "  To run live flows, start the server and set:"
echo "    export PIKSHIPP_TEST_TOKEN=<session token from Google OAuth>"
echo "    ./scripts/user_flows.sh"
echo ""
echo "  Prometheus metrics:  $BASE/metrics"
echo "  Health:              $BASE/healthz"
echo "  OpenAPI spec:        cat api/openapi.yaml"
