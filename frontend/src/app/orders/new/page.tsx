"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PhoneInput, isValidIndianMobile } from "@/components/ui/PhoneInput";
import { PincodeInput } from "@/components/ui/PincodeInput";
import {
  catalogApi,
  ordersApi,
  paiseToRupees,
  pricingApi,
  type BuyerAddress,
  type BuyerAddressInput,
  type PickupLocation,
  type PricingQuote,
} from "@/lib/api";
import { Calculator, Loader2, Package as PackageIcon, Plus, Trash2, Star, Warehouse } from "lucide-react";
import {
  WarehouseForm,
  EMPTY_WAREHOUSE,
  type WarehouseFormValue,
} from "@/components/account/WarehouseForm";

type Line = { sku: string; name: string; quantity: number; price: number; weight: number };

// Dimension presets — chips below the L×W×H inputs. cm.
const PACKAGE_PRESETS = [
  { label: "Envelope",  l: 25, w: 18, h: 2  },
  { label: "Small box", l: 20, w: 15, h: 10 },
  { label: "Medium",    l: 30, w: 20, h: 15 },
  { label: "Large",     l: 40, w: 30, h: 25 },
];

export default function NewOrderPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const router = useRouter();
  const [pickups, setPickups] = React.useState<PickupLocation[]>([]);
  const [savedAddresses, setSavedAddresses] = React.useState<BuyerAddress[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const [pickupID, setPickupID] = React.useState("");
  // When the user picks "+ New warehouse" we expand an inline form. On save we
  // POST it, get the new pickup back, set pickupID to its ID, and collapse.
  const [showNewWarehouse, setShowNewWarehouse] = React.useState(false);
  const [newWarehouse, setNewWarehouse] = React.useState<WarehouseFormValue>(EMPTY_WAREHOUSE);
  const [savingWarehouse, setSavingWarehouse] = React.useState(false);
  const [warehouseErr, setWarehouseErr] = React.useState<string | null>(null);
  const [channel, setChannel] = React.useState("manual");
  const [channelOrderID, setChannelOrderID] = React.useState("");
  // "" means "new buyer" (fill the form below); otherwise the saved-address ID.
  const [savedAddrID, setSavedAddrID] = React.useState<string>("");
  const [saveToBook, setSaveToBook] = React.useState(false);
  const [addrLabel, setAddrLabel] = React.useState("");
  const [buyerName, setBuyerName] = React.useState("");
  const [buyerPhone, setBuyerPhone] = React.useState("+91");
  const [buyerEmail, setBuyerEmail] = React.useState("");
  const [shipLine1, setShipLine1] = React.useState("");
  const [shipLine2, setShipLine2] = React.useState("");
  const [shipCity, setShipCity] = React.useState("");
  const [shipState, setShipState] = React.useState("");
  const [shipPincode, setShipPincode] = React.useState("");
  const [paymentMethod, setPaymentMethod] = React.useState<"prepaid" | "cod">("prepaid");

  const [lines, setLines] = React.useState<Line[]>([
    { sku: "", name: "", quantity: 1, price: 0, weight: 100 },
  ]);

  // Package dimensions in cm (most Indian sellers think in cm). Converted
  // to mm on submit because the backend stores mm. Defaults match what was
  // hardcoded earlier — a small parcel.
  const [packageL, setPackageL] = React.useState(20);
  const [packageW, setPackageW] = React.useState(15);
  const [packageH, setPackageH] = React.useState(10);

  // Live shipping quote from the pricing engine. The seller declares item
  // value; we compute the actual shipping price they (or the buyer in COD)
  // will pay. Recomputes when the route, dimensions, weight, or payment mode
  // changes — debounced so typing doesn't hammer the backend.
  const [quote, setQuote] = React.useState<PricingQuote | null>(null);
  const [quoting, setQuoting] = React.useState(false);
  const [quoteErr, setQuoteErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    Promise.all([catalogApi.listPickups(), catalogApi.listBuyerAddresses()])
      .then(([p, a]) => {
        const pickupList = p || [];
        const addrList = a || [];
        setPickups(pickupList);
        setSavedAddresses(addrList);
        const def = pickupList.find((x) => x.is_default) || pickupList[0];
        if (def) setPickupID(def.id);
        const defAddr = addrList.find((x) => x.is_default);
        if (defAddr) applySavedAddress(defAddr);
        setLoading(false);
      })
      .catch((e) => {
        setError((e as { message?: string }).message || "Failed to load");
        setLoading(false);
      });
    // applySavedAddress is stable for our purposes — it only reads from
    // function arguments and sets pure useState setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function applySavedAddress(a: BuyerAddress) {
    setSavedAddrID(a.id);
    setSaveToBook(false);
    setBuyerName(a.buyer_name);
    setBuyerPhone(a.buyer_phone);
    setBuyerEmail(a.buyer_email || "");
    setShipLine1(a.address.line1);
    setShipLine2(a.address.line2 || "");
    setShipCity(a.address.city);
    setShipState(a.state);
    setShipPincode(a.pincode);
  }

  async function saveNewWarehouse() {
    setSavingWarehouse(true);
    setWarehouseErr(null);
    try {
      const created = await catalogApi.createPickup(newWarehouse);
      setPickups((cur) => [...cur, created]);
      setPickupID(created.id);
      setShowNewWarehouse(false);
      setNewWarehouse(EMPTY_WAREHOUSE);
    } catch (e) {
      setWarehouseErr((e as { message?: string }).message || "Failed to save warehouse");
    } finally {
      setSavingWarehouse(false);
    }
  }

  function clearAddressForm() {
    setSavedAddrID("");
    setAddrLabel("");
    setBuyerName("");
    setBuyerPhone("+91");
    setBuyerEmail("");
    setShipLine1("");
    setShipLine2("");
    setShipCity("");
    setShipState("");
    setShipPincode("");
  }

  const subtotal = lines.reduce((s, l) => s + l.price * l.quantity * 100, 0);
  const totalWeight = lines.reduce((s, l) => s + l.weight * l.quantity, 0);
  const shippingPaise = quote?.total_paise ?? 0;
  const totalPaise = subtotal + shippingPaise;

  const pickupPincode =
    pickups.find((p) => p.id === pickupID)?.pincode ?? "";

  // Pre-conditions for asking the pricing engine for a quote: route is fully
  // known, weight is positive, dimensions are positive.
  const canQuote =
    /^[1-9]\d{5}$/.test(pickupPincode) &&
    /^[1-9]\d{5}$/.test(shipPincode) &&
    totalWeight > 0 &&
    packageL >= 1 && packageW >= 1 && packageH >= 1;

  // Debounced auto-quote. Anything that changes the route, package, or
  // payment mode resets the quote and re-fetches 400ms later. AbortController
  // cancels in-flight requests when inputs change again before the response.
  React.useEffect(() => {
    if (!canQuote) {
      setQuote(null);
      setQuoteErr(null);
      return;
    }
    setQuoting(true);
    setQuoteErr(null);
    const ctrl = new AbortController();
    const t = setTimeout(async () => {
      try {
        const res = await pricingApi.quote({
          pickup_pincode: pickupPincode,
          ship_to_pincode: shipPincode,
          payment_mode: paymentMethod,
          declared_value_paise: subtotal,
          packages: [
            {
              weight_g: totalWeight,
              length_mm: packageL * 10,
              width_mm: packageW * 10,
              height_mm: packageH * 10,
            },
          ],
        });
        if (ctrl.signal.aborted) return;
        const cheapest = [...(res.quotes || [])].sort(
          (a, b) => a.total_paise - b.total_paise,
        )[0];
        setQuote(cheapest ?? null);
        if (!cheapest) {
          setQuoteErr("No carrier prices this route yet — try a different pincode pair or contact support.");
        }
      } catch (e) {
        if (ctrl.signal.aborted) return;
        setQuote(null);
        setQuoteErr((e as { message?: string }).message || "Failed to fetch shipping quote");
      } finally {
        if (!ctrl.signal.aborted) setQuoting(false);
      }
    }, 400);
    return () => {
      ctrl.abort();
      clearTimeout(t);
    };
  }, [canQuote, pickupPincode, shipPincode, paymentMethod, totalWeight, packageL, packageW, packageH, subtotal]);

  const canSubmit =
    buyerName.trim() !== "" &&
    isValidIndianMobile(buyerPhone) &&
    pickupID !== "" &&
    shipLine1.trim() !== "" &&
    shipCity.trim() !== "" &&
    shipState.trim() !== "" &&
    /^[1-9]\d{5}$/.test(shipPincode) &&
    lines.length > 0 &&
    lines.every((l) => l.quantity >= 1 && l.weight >= 1) &&
    packageL >= 1 && packageW >= 1 && packageH >= 1 &&
    quote !== null;

  function setLine(i: number, patch: Partial<Line>) {
    setLines((cur) => cur.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));
  }
  function addLine() {
    setLines((cur) => [...cur, { sku: "", name: "", quantity: 1, price: 0, weight: 100 }]);
  }
  function removeLine(i: number) {
    setLines((cur) => cur.filter((_, idx) => idx !== i));
  }

  // Stable channel_order_id for retries — without this, two rapid submits
  // produce different "WEB-<timestamp>" keys and bypass backend idempotency.
  const fallbackChannelOrderID = React.useRef(`WEB-${Date.now()}`);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (submitting) return; // belt-and-suspenders against double-submit
    setError(null);
    setSubmitting(true);
    try {
      const ship = {
        line1: shipLine1,
        line2: shipLine2 || undefined,
        city: shipCity,
        state: shipState,
        country: "IN",
        pincode: shipPincode,
      };
      // Persist to address book first when requested. We do this before
      // creating the order so a failed save doesn't dangle behind a
      // successful order — and a failed order doesn't dangle a book entry.
      if (!savedAddrID && saveToBook && addrLabel.trim()) {
        const payload: BuyerAddressInput = {
          label: addrLabel.trim(),
          buyer_name: buyerName,
          buyer_phone: buyerPhone,
          buyer_email: buyerEmail || undefined,
          address: ship,
          pincode: shipPincode,
          state: shipState,
          is_default: false,
        };
        try {
          await catalogApi.createBuyerAddress(payload);
        } catch (e) {
          // Saving the book entry isn't worth blocking the order on. Surface
          // it as a warning but keep going.
          console.warn("save buyer_address failed:", (e as { message?: string }).message);
        }
      }
      const order = await ordersApi.create({
        channel,
        channel_order_id: channelOrderID || fallbackChannelOrderID.current,
        order_ref: "",
        buyer_name: buyerName,
        buyer_phone: buyerPhone,
        buyer_email: buyerEmail || "",
        billing_address: ship,
        shipping_address: ship,
        shipping_pincode: shipPincode,
        shipping_state: shipState,
        payment_method: paymentMethod,
        subtotal_paise: subtotal,
        shipping_paise: shippingPaise,
        discount_paise: 0,
        tax_paise: 0,
        total_paise: totalPaise,
        cod_amount_paise: paymentMethod === "cod" ? totalPaise : 0,
        pickup_location_id: pickupID,
        package_weight_g: Math.max(100, totalWeight),
        package_length_mm: packageL * 10,
        package_width_mm: packageW * 10,
        package_height_mm: packageH * 10,
        notes: "",
        tags: [],
        lines: lines.map((l, i) => ({
          line_no: i + 1,
          sku: l.sku || `LINE-${i + 1}`,
          name: l.name || l.sku || `Line ${i + 1}`,
          quantity: l.quantity,
          unit_price_paise: Math.round(l.price * 100),
          unit_weight_g: l.weight,
          hsn_code: "",
          category_hint: "",
        })),
      });
      router.replace(`/orders/${order.id}`);
    } catch (e) {
      const errObj = e as { message?: string; status?: number };
      if (errObj.status === 429) {
        setError(errObj.message || "You've hit your daily order limit. Upgrade to enterprise to lift it.");
      } else {
        setError(errObj.message || "Failed to create order");
      }
      setSubmitting(false);
    }
  }

  if (loading) {
    return <div className="text-sm text-muted">Loading…</div>;
  }

  // When the seller has no warehouses yet, surface the "+ New warehouse" form
  // expanded by default so they can add one inline without being bounced to
  // /warehouses/new. The rest of the form stays usable below.
  const noWarehousesYet = pickups.length === 0;

  return (
    <form onSubmit={submit} className="mx-auto max-w-3xl space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Create order</h1>
        <p className="mt-1 text-sm text-muted">Add buyer details, items, and we'll allocate the best courier.</p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Warehouse className="h-4 w-4 text-muted" />
            Pickup warehouse
          </CardTitle>
          <CardDescription>
            Where the courier will collect this shipment. Pick a saved warehouse
            or add a new one — both get saved to your address book.
          </CardDescription>
        </CardHeader>
        <CardBody className="space-y-4">
          <div className="grid gap-2 sm:grid-cols-2">
            {pickups.map((p) => {
              const active = pickupID === p.id;
              return (
                <button
                  type="button"
                  key={p.id}
                  onClick={() => {
                    setPickupID(p.id);
                    setShowNewWarehouse(false);
                  }}
                  className={
                    "rounded-md border p-3 text-left text-sm transition-colors " +
                    (active
                      ? "border-accent bg-accent/5"
                      : "border-border bg-surface hover:bg-bg")
                  }
                >
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{p.label}</span>
                    {p.is_default && (
                      <span className="inline-flex items-center gap-0.5 rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                        <Star className="h-2.5 w-2.5" /> Default
                      </span>
                    )}
                    {!p.active && (
                      <span className="rounded bg-warning/10 px-1.5 py-0.5 text-[10px] font-medium text-warning">
                        Inactive
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-muted">
                    {p.contact_name} · {p.contact_phone}
                  </div>
                  <div className="truncate text-xs text-muted">
                    {p.address.line1}, {p.address.city} {p.pincode}
                  </div>
                </button>
              );
            })}
            <button
              type="button"
              onClick={() => {
                setPickupID("");
                setShowNewWarehouse(true);
              }}
              className={
                "flex items-center justify-center gap-1 rounded-md border border-dashed p-3 text-sm " +
                (showNewWarehouse || noWarehousesYet
                  ? "border-accent bg-accent/5 text-accent"
                  : "border-border text-muted hover:bg-bg")
              }
            >
              <Plus className="h-4 w-4" /> New warehouse
            </button>
          </div>

          {(showNewWarehouse || noWarehousesYet) && (
            <div className="rounded-md border border-dashed border-border bg-bg/30 p-4">
              {noWarehousesYet && (
                <p className="mb-3 text-sm text-muted">
                  No warehouses yet — add your first one to send this order. It'll
                  be saved for future orders.
                </p>
              )}
              {warehouseErr && (
                <p className="mb-3 text-sm text-danger">{warehouseErr}</p>
              )}
              <WarehouseForm
                value={newWarehouse}
                onChange={setNewWarehouse}
                onSubmit={saveNewWarehouse}
                onCancel={
                  noWarehousesYet
                    ? undefined
                    : () => {
                        setShowNewWarehouse(false);
                        setNewWarehouse(EMPTY_WAREHOUSE);
                      }
                }
                submitLabel="Save & use this warehouse"
                submitting={savingWarehouse}
              />
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Buyer & shipping</CardTitle>
          <CardDescription>
            Pick from your saved addresses or enter a new one. New entries can
            be saved to your address book for next time.
          </CardDescription>
        </CardHeader>
        <CardBody className="space-y-4">
          {savedAddresses.length > 0 && (
            <div>
              <div className="mb-2 flex items-center justify-between">
                <span className="text-sm font-medium">Saved addresses</span>
                <a
                  href="/account?tab=addresses"
                  className="text-xs text-accent hover:underline"
                >
                  Manage
                </a>
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                {savedAddresses.map((a) => {
                  const active = savedAddrID === a.id;
                  return (
                    <button
                      type="button"
                      key={a.id}
                      onClick={() => applySavedAddress(a)}
                      className={
                        "rounded-md border p-3 text-left text-sm transition-colors " +
                        (active
                          ? "border-accent bg-accent/5"
                          : "border-border bg-surface hover:bg-bg")
                      }
                    >
                      <div className="flex items-center gap-2">
                        <span className="font-medium">{a.label}</span>
                        {a.is_default && (
                          <span className="inline-flex items-center gap-0.5 rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                            <Star className="h-2.5 w-2.5" /> Default
                          </span>
                        )}
                      </div>
                      <div className="text-xs text-muted">
                        {a.buyer_name} · {a.buyer_phone}
                      </div>
                      <div className="truncate text-xs text-muted">
                        {a.address.line1}, {a.address.city} {a.pincode}
                      </div>
                    </button>
                  );
                })}
                <button
                  type="button"
                  onClick={clearAddressForm}
                  className={
                    "flex items-center justify-center gap-1 rounded-md border border-dashed p-3 text-sm " +
                    (savedAddrID === ""
                      ? "border-accent bg-accent/5 text-accent"
                      : "border-border text-muted hover:bg-bg")
                  }
                >
                  <Plus className="h-4 w-4" /> New address
                </button>
              </div>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Recipient name">
              <Input
                required
                value={buyerName}
                onChange={(e) => setBuyerName(e.target.value)}
              />
            </Field>
            <Field
              label="Phone"
              error={
                buyerPhone !== "+91" && !isValidIndianMobile(buyerPhone)
                  ? "10 digits, starts with 6/7/8/9"
                  : undefined
              }
            >
              <PhoneInput value={buyerPhone} onChange={setBuyerPhone} required />
            </Field>
            <Field label="Email" hint="Optional — used for tracking notifications">
              <Input
                type="email"
                value={buyerEmail}
                onChange={(e) => setBuyerEmail(e.target.value)}
              />
            </Field>
            <Field label="Pincode">
              <PincodeInput
                value={shipPincode}
                onChange={setShipPincode}
                onResolve={({ city, state }) => {
                  if (!shipCity) setShipCity(city);
                  if (!shipState) setShipState(state);
                }}
                required
              />
            </Field>
            <Field label="Address line 1" hint="Street, locality">
              <Input
                required
                value={shipLine1}
                onChange={(e) => setShipLine1(e.target.value)}
              />
            </Field>
            <Field label="Address line 2" hint="Optional">
              <Input
                value={shipLine2}
                onChange={(e) => setShipLine2(e.target.value)}
              />
            </Field>
            <Field label="City">
              <Input
                required
                value={shipCity}
                onChange={(e) => setShipCity(e.target.value)}
              />
            </Field>
            <Field label="State">
              <Input
                required
                value={shipState}
                onChange={(e) => setShipState(e.target.value)}
              />
            </Field>
          </div>

          {savedAddrID === "" && (
            <div className="rounded-md border border-dashed border-border bg-bg/30 p-3">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={saveToBook}
                  onChange={(e) => setSaveToBook(e.target.checked)}
                  className="h-4 w-4 rounded border-border text-accent focus:ring-accent"
                />
                Save this to my address book for next time
              </label>
              {saveToBook && (
                <div className="mt-2">
                  <Field label="Label" hint="e.g. Mom's home, Bangalore office">
                    <Input
                      required
                      placeholder="Home"
                      value={addrLabel}
                      onChange={(e) => setAddrLabel(e.target.value)}
                    />
                  </Field>
                </div>
              )}
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Order details</CardTitle>
          <CardDescription>Channel, payment mode, and pickup origin.</CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Channel">
            <select
              value={channel}
              onChange={(e) => setChannel(e.target.value)}
              className="h-10 w-full rounded-md border border-border bg-surface px-3 text-sm"
            >
              <option value="manual">Manual / Direct</option>
              <option value="shopify">Shopify</option>
              <option value="csv">CSV import</option>
            </select>
          </Field>
          <Field label="Channel order ID" hint="Leave blank to auto-generate">
            <Input value={channelOrderID} onChange={(e) => setChannelOrderID(e.target.value)} />
          </Field>
          <Field label="Payment">
            <select
              value={paymentMethod}
              onChange={(e) => setPaymentMethod(e.target.value as "prepaid" | "cod")}
              className="h-10 w-full rounded-md border border-border bg-surface px-3 text-sm"
            >
              <option value="prepaid">Prepaid</option>
              <option value="cod">Cash on Delivery</option>
            </select>
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Items</CardTitle>
          <CardDescription>
            What's inside the package. Item value is the <strong>declared
            worth of goods</strong> — used for the invoice and (for COD
            orders) what the buyer pays. Shipping is calculated separately
            below.
          </CardDescription>
        </CardHeader>
        <CardBody className="space-y-3">
          {lines.map((l, i) => {
            const lineSubtotal = l.price * l.quantity;
            return (
              <div
                key={i}
                className="rounded-lg border border-border bg-bg/30 p-4 transition-colors hover:bg-bg/60"
              >
                <div className="mb-3 flex items-center justify-between">
                  <span className="text-xs font-medium uppercase tracking-wider text-muted">
                    Item {i + 1}
                  </span>
                  {lines.length > 1 && (
                    <button
                      type="button"
                      onClick={() => removeLine(i)}
                      aria-label={`Remove item ${i + 1}`}
                      className="rounded-md p-1 text-muted hover:bg-danger/10 hover:text-danger"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  )}
                </div>

                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="SKU" hint="Optional — auto-generated if blank">
                    <Input
                      placeholder="TSHIRT-RED-M"
                      value={l.sku}
                      onChange={(e) => setLine(i, { sku: e.target.value })}
                    />
                  </Field>
                  <Field label="Product name">
                    <Input
                      placeholder="Cotton t-shirt"
                      value={l.name}
                      onChange={(e) => setLine(i, { name: e.target.value })}
                    />
                  </Field>
                </div>

                <div className="mt-3 grid gap-3 grid-cols-3">
                  <Field label="Quantity">
                    <Input
                      type="number"
                      inputMode="numeric"
                      className="no-spin"
                      min={1}
                      value={l.quantity}
                      onChange={(e) => setLine(i, { quantity: Math.max(1, Number(e.target.value) || 0) })}
                    />
                  </Field>
                  <Field label="Declared value (₹)">
                    <Input
                      type="number"
                      inputMode="decimal"
                      className="no-spin"
                      min={0}
                      step="0.01"
                      value={l.price}
                      onChange={(e) => setLine(i, { price: Math.max(0, Number(e.target.value) || 0) })}
                    />
                  </Field>
                  <Field label="Unit weight (g)">
                    <Input
                      type="number"
                      inputMode="numeric"
                      className="no-spin"
                      min={1}
                      value={l.weight}
                      onChange={(e) => setLine(i, { weight: Math.max(1, Number(e.target.value) || 0) })}
                    />
                  </Field>
                </div>

                <div className="mt-3 flex items-center justify-end gap-2 text-xs text-muted">
                  Line total
                  <span className="font-medium text-text">
                    {paiseToRupees(Math.round(lineSubtotal * 100))}
                  </span>
                </div>
              </div>
            );
          })}
          <Button type="button" variant="ghost" onClick={addLine}>
            <Plus className="h-4 w-4" /> Add another item
          </Button>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <PackageIcon className="h-4 w-4 text-muted" />
            Package
          </CardTitle>
          <CardDescription>
            Outer dimensions of the box you'll hand to the courier. Pick a preset or
            enter your own — couriers price by both weight and volume.
          </CardDescription>
        </CardHeader>
        <CardBody className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Length (cm)">
              <Input
                type="number"
                inputMode="numeric"
                className="no-spin"
                min={1}
                value={packageL}
                onChange={(e) => setPackageL(Math.max(0, Number(e.target.value) || 0))}
              />
            </Field>
            <Field label="Width (cm)">
              <Input
                type="number"
                inputMode="numeric"
                className="no-spin"
                min={1}
                value={packageW}
                onChange={(e) => setPackageW(Math.max(0, Number(e.target.value) || 0))}
              />
            </Field>
            <Field label="Height (cm)">
              <Input
                type="number"
                inputMode="numeric"
                className="no-spin"
                min={1}
                value={packageH}
                onChange={(e) => setPackageH(Math.max(0, Number(e.target.value) || 0))}
              />
            </Field>
          </div>

          <div className="flex flex-wrap gap-2">
            {PACKAGE_PRESETS.map((p) => {
              const active = packageL === p.l && packageW === p.w && packageH === p.h;
              return (
                <button
                  type="button"
                  key={p.label}
                  onClick={() => { setPackageL(p.l); setPackageW(p.w); setPackageH(p.h); }}
                  className={
                    "rounded-full border px-3 py-1 text-xs transition-colors " +
                    (active
                      ? "border-accent bg-accent/10 text-accent"
                      : "border-border bg-surface text-muted hover:bg-bg")
                  }
                >
                  {p.label}
                  <span className="ml-1 text-muted/70">{p.l}×{p.w}×{p.h}</span>
                </button>
              );
            })}
          </div>

          <div className="flex items-center justify-between rounded-md border border-dashed border-border bg-bg/30 px-4 py-2 text-sm">
            <div>
              <div className="font-medium">Total weight</div>
              <div className="text-xs text-muted">Auto from item weight × quantity</div>
            </div>
            <div className="text-base font-semibold tabular-nums">
              {totalWeight.toLocaleString("en-IN")} g
              {totalWeight >= 1000 && (
                <span className="ml-1 text-xs font-normal text-muted">
                  ({(totalWeight / 1000).toFixed(2)} kg)
                </span>
              )}
            </div>
          </div>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Calculator className="h-4 w-4 text-muted" />
            Shipping
          </CardTitle>
          <CardDescription>
            Calculated from your route, package size, and weight. You don't set
            this — we do. Refreshes as you change inputs above.
          </CardDescription>
        </CardHeader>
        <CardBody>
          {!canQuote && (
            <p className="text-sm text-muted">
              Fill in pickup warehouse, ship-to pincode, package dimensions, and
              item weights to see a shipping quote.
            </p>
          )}
          {canQuote && quoting && (
            <div className="flex items-center gap-2 text-sm text-muted">
              <Loader2 className="h-4 w-4 animate-spin" /> Getting quote…
            </div>
          )}
          {canQuote && !quoting && quoteErr && (
            <div className="rounded-md border border-danger/20 bg-danger/5 px-3 py-2 text-sm text-danger">
              {quoteErr}
            </div>
          )}
          {canQuote && !quoting && quote && (
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="flex items-center gap-2">
                  <span className="font-medium capitalize">{quote.carrier_code || "carrier"}</span>
                  <span className="rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-accent">
                    {quote.service_type}
                  </span>
                  {quote.zone && (
                    <span className="rounded bg-bg px-1.5 py-0.5 text-[10px] font-medium text-muted">
                      Zone {quote.zone}
                    </span>
                  )}
                </div>
                <div className="mt-0.5 text-xs text-muted">
                  {quote.estimated_days} day{quote.estimated_days === 1 ? "" : "s"} estimated · {totalWeight.toLocaleString("en-IN")} g chargeable
                </div>
              </div>
              <div className="text-right">
                <div className="text-xl font-semibold tabular-nums">
                  {paiseToRupees(quote.total_paise)}
                </div>
                <div className="text-xs text-muted">shipping</div>
              </div>
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardBody className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm">
            <div className="flex justify-between gap-6 text-muted">
              <span>Items subtotal</span>
              <span className="font-medium text-text tabular-nums">{paiseToRupees(subtotal)}</span>
            </div>
            <div className="flex justify-between gap-6 text-muted">
              <span>Shipping</span>
              <span className="font-medium text-text tabular-nums">
                {quote ? paiseToRupees(shippingPaise) : "—"}
              </span>
            </div>
            <div className="mt-1 flex justify-between gap-6 border-t border-border pt-1">
              <span className="font-semibold">Total</span>
              <span className="font-semibold tabular-nums">{paiseToRupees(totalPaise)}</span>
            </div>
            <div className="mt-1 text-xs text-muted">
              {paymentMethod === "cod"
                ? `${paiseToRupees(totalPaise)} to collect from buyer at delivery`
                : "Prepaid — seller is billed shipping; items are between seller and buyer"}
            </div>
          </div>
          <div className="flex flex-col items-end gap-1 sm:items-end">
            {error && (
              <div className="rounded-md border border-danger/20 bg-danger/5 px-3 py-1.5 text-xs text-danger">
                {error}
              </div>
            )}
            <Button type="submit" loading={submitting} disabled={!canSubmit}>
              Create order
            </Button>
          </div>
        </CardBody>
      </Card>
    </form>
  );
}
