// Pikshipp API client.
//
// In dev, requests go through Next's rewrite proxy (/api/v1/*) which forwards
// to NEXT_PUBLIC_API_URL. In prod, the same convention works behind any
// reverse proxy that maps /v1/* to the backend.

const BASE = "/api"; // proxied by next.config.mjs

export type ApiError = {
  status: number;
  message: string;
  body?: unknown;
};

function readToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem("pikshipp_token");
}

export function setToken(token: string | null) {
  if (typeof window === "undefined") return;
  if (token) window.localStorage.setItem("pikshipp_token", token);
  else window.localStorage.removeItem("pikshipp_token");
}

export function getToken(): string | null {
  return readToken();
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: { auth?: boolean } = { auth: true },
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (opts.auth !== false) {
    const tok = readToken();
    if (tok) headers["Authorization"] = `Bearer ${tok}`;
  }

  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  let parsed: unknown = undefined;
  const text = await res.text();
  if (text) {
    try { parsed = JSON.parse(text); } catch { parsed = text; }
  }

  if (!res.ok) {
    let errMessage = `HTTP ${res.status}`;
    if (parsed && typeof parsed === "object") {
      const e = (parsed as { error?: unknown }).error;
      if (typeof e === "string" && e) errMessage = e;
    }
    // 401 from an authenticated call → token is dead. Clear it and notify
    // the session provider so it can route the user to /login.
    if (res.status === 401 && opts.auth !== false && typeof window !== "undefined") {
      setToken(null);
      window.dispatchEvent(new CustomEvent("pikshipp:unauthorized"));
    }
    const err: ApiError = { status: res.status, message: errMessage, body: parsed };
    throw err;
  }
  return parsed as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),

  // No-auth variants for public endpoints (login).
  postPublic: <T>(path: string, body?: unknown) =>
    request<T>("POST", path, body, { auth: false }),
};

// ─── Typed responses ────────────────────────────────────────────────────────

export type User = {
  id: string;
  email: string;
  name: string;
  status: string;
  kind: string;
  created_at: string;
};

export type SellerMembership = {
  user_id: string;
  seller_id: string;
  roles: string[];
  status: string;
};

export type Seller = {
  id: string;
  legal_name: string;
  display_name: string;
  seller_type: "small_business" | "mid_market" | "enterprise";
  lifecycle_state: "provisioning" | "sandbox" | "active" | "suspended" | "wound_down";
  gstin?: string;
  pan?: string;
  billing_email: string;
  support_email: string;
  primary_phone: string;
  signup_source: string;
  founding_user_id: string;
  suspended_reason?: string;
  created_at: string;
  updated_at: string;
};

export type Address = {
  line1: string;
  line2?: string;
  city: string;
  state: string;
  country: string;
  pincode: string;
};

export type BuyerAddress = {
  id: string;
  seller_id: string;
  label: string;
  buyer_name: string;
  buyer_phone: string;
  buyer_email?: string;
  address: Address;
  pincode: string;
  state: string;
  is_default: boolean;
  created_at: string;
  updated_at: string;
};

export type BuyerAddressInput = Omit<
  BuyerAddress,
  "id" | "seller_id" | "created_at" | "updated_at"
>;

export type PickupLocation = {
  id: string;
  seller_id: string;
  label: string;
  contact_name: string;
  contact_phone: string;
  contact_email?: string;
  address: Address;
  pincode: string;
  state: string;
  pickup_hours?: string;
  gstin?: string;
  active: boolean;
  is_default: boolean;
  created_at: string;
  updated_at: string;
};

export type OrderLine = {
  line_no: number;
  sku: string;
  name: string;
  quantity: number;
  unit_price_paise: number;
  unit_weight_g: number;
  hsn_code?: string;
  category_hint?: string;
};

export type Order = {
  id: string;
  seller_id: string;
  state: "draft" | "ready" | "allocating" | "booked" | "in_transit" | "delivered" | "closed" | "cancelled" | "rto";
  channel: string;
  channel_order_id: string;
  order_ref?: string;
  buyer_name: string;
  buyer_phone: string;
  buyer_email?: string;
  billing_address: Address;
  shipping_address: Address;
  shipping_pincode: string;
  shipping_state: string;
  payment_method: "prepaid" | "cod";
  subtotal_paise: number;
  shipping_paise: number;
  discount_paise: number;
  tax_paise: number;
  total_paise: number;
  cod_amount_paise: number;
  pickup_location_id: string;
  package_weight_g: number;
  package_length_mm: number;
  package_width_mm: number;
  package_height_mm: number;
  awb_number?: string;
  carrier_code?: string;
  booked_at?: string;
  notes?: string;
  tags?: string[];
  lines: OrderLine[];
  created_at: string;
  updated_at: string;
};

export type WalletBalance = {
  seller_id: string;
  balance: number;
  hold_total: number;
  available: number;
  credit_limit: number;
  grace_amount: number;
  status: string;
};

export type Usage = {
  shipments_this_month: number;
  shipment_month_limit: number;
  orders_today: number;
  order_day_limit: number;
};

export type Contract = {
  id: string;
  seller_id: string;
  version: number;
  state: "draft" | "active" | "superseded" | "terminated";
  rate_card_id?: string;
  terms: Record<string, unknown>;
  effective_from: string;
  effective_to?: string;
  signed_at?: string;
  created_by: string;
  activated_at?: string;
  terminated_at?: string;
  termination_reason?: string;
  created_at: string;
  updated_at: string;
};

// ─── Endpoint helpers ──────────────────────────────────────────────────────

export const auth = {
  devLogin: (email: string, name: string) =>
    api.postPublic<{ token: string; user: User; sellers: SellerMembership[] | null; expires_at: string }>(
      "/v1/auth/dev-login",
      { email, name },
    ),
  me: () => api.get<{ user: User; sellers: SellerMembership[] | null; active_seller_id: string }>("/v1/me"),
  logout: () => api.post<{ status: string }>("/v1/auth/logout"),
  selectSeller: (sellerId: string) =>
    api.post<{ token: string; expires_at: string; membership: SellerMembership }>(
      "/v1/auth/select-seller",
      { seller_id: sellerId },
    ),
};

export const sellers = {
  provision: (input: {
    legal_name: string;
    display_name: string;
    primary_phone: string;
    billing_email?: string;
    support_email?: string;
  }) => api.post<{ seller: Seller; token: string; expires_at: string }>("/v1/sellers", input),

  get: () => api.get<Seller>("/v1/seller"),

  submitKYC: (input: {
    legal_name: string;
    gstin: string;
    pan: string;
  }) => api.post<{ status: string }>("/v1/seller/kyc", input),

  upgrade: (sellerId: string, body: {
    new_type: string;
    terms: Record<string, unknown>;
    rate_card_id?: string;
  }) => api.post<{ contract: Contract; new_type: string; seller_id: string }>(
    `/v1/admin/sellers/${sellerId}/upgrade`, body,
  ),

  contract: () => api.get<Contract>("/v1/seller/contract"),
  contracts: () => api.get<Contract[]>("/v1/seller/contracts"),
  usage: () => api.get<Usage>("/v1/seller/usage"),
};

export const catalogApi = {
  listPickups: () => api.get<PickupLocation[]>("/v1/pickup-locations"),
  createPickup: (input: Omit<PickupLocation, "id" | "seller_id" | "created_at" | "updated_at">) =>
    api.post<PickupLocation>("/v1/pickup-locations", input),

  listBuyerAddresses: () => api.get<BuyerAddress[]>("/v1/buyer-addresses"),
  createBuyerAddress: (input: BuyerAddressInput) =>
    api.post<BuyerAddress>("/v1/buyer-addresses", input),
  updateBuyerAddress: (id: string, patch: Partial<BuyerAddressInput>) =>
    api.patch<BuyerAddress>(`/v1/buyer-addresses/${id}`, patch),
  deleteBuyerAddress: (id: string) => api.del<void>(`/v1/buyer-addresses/${id}`),
  setDefaultBuyerAddress: (id: string) =>
    api.post<void>(`/v1/buyer-addresses/${id}/default`),
};

export const ordersApi = {
  list: () => api.get<{ orders: Order[]; total: number }>("/v1/orders"),
  create: (req: Omit<Order, "id" | "seller_id" | "state" | "created_at" | "updated_at" | "awb_number" | "carrier_code" | "booked_at">) =>
    api.post<Order>("/v1/orders", req),
  get: (id: string) => api.get<Order>(`/v1/orders/${id}`),
  cancel: (id: string, reason: string) =>
    api.post<{ status: string }>(`/v1/orders/${id}/cancel`, { reason }),
  book: (id: string) => api.post<Shipment>(`/v1/orders/${id}/book`),
};

export type Shipment = {
  ID: string;
  OrderID: string;
  SellerID: string;
  State: string;
  CarrierCode: string;
  ServiceType: string;
  AWB: string;
  CarrierShipmentID: string;
  ChargesPaise: number;
  CODAmountPaise: number;
  BookedAt: string | null;
};

export type TrackingEvent = {
  ShipmentID: string;
  SellerID: string;
  CarrierCode: string;
  AWB: string;
  RawStatus: string;
  CanonicalStatus: string;
  Location: string;
  OccurredAt: string;
  Source: string;
  RawPayload?: Record<string, unknown>;
};

export const shipmentsApi = {
  listEvents: (shipmentID: string) =>
    api.get<TrackingEvent[] | null>(`/v1/shipments/${shipmentID}/tracking-events`),
  refresh: (shipmentID: string) =>
    api.post<TrackingEvent[] | null>(`/v1/shipments/${shipmentID}/refresh`),
};

export type PricingPackage = {
  weight_g: number;
  length_mm: number;
  width_mm: number;
  height_mm: number;
};

export type PricingQuote = {
  carrier_id: string;
  carrier_code: string;
  service_type: "standard" | "express" | "same_day" | "lite";
  estimated_days: number;
  total_paise: number;
  zone?: string;
  packages: number;
  breakdown?: Record<string, number>;
};

export const pricingApi = {
  quote: (input: {
    pickup_pincode: string;
    ship_to_pincode: string;
    payment_mode?: "prepaid" | "cod";
    declared_value_paise?: number;
    packages: PricingPackage[];
  }) =>
    api.post<{ quotes: PricingQuote[] }>("/v1/pricing/quote", input),
};

export const walletApi = {
  balance: () => api.get<WalletBalance>("/v1/wallet/balance"),
};

// ─── Helpers ────────────────────────────────────────────────────────────────

export function paiseToRupees(paise: number): string {
  const rupees = paise / 100;
  return new Intl.NumberFormat("en-IN", {
    style: "currency",
    currency: "INR",
    maximumFractionDigits: 2,
  }).format(rupees);
}
