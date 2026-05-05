"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PhoneInput, isValidIndianMobile } from "@/components/ui/PhoneInput";
import { PincodeInput } from "@/components/ui/PincodeInput";
import { catalogApi, ordersApi, paiseToRupees, type PickupLocation } from "@/lib/api";
import { MapPin, Package as PackageIcon, Plus, Trash2 } from "lucide-react";

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
  const [loading, setLoading] = React.useState(true);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const [pickupID, setPickupID] = React.useState("");
  const [channel, setChannel] = React.useState("manual");
  const [channelOrderID, setChannelOrderID] = React.useState("");
  const [buyerName, setBuyerName] = React.useState("");
  const [buyerPhone, setBuyerPhone] = React.useState("+91");
  const [shipLine1, setShipLine1] = React.useState("");
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

  React.useEffect(() => {
    catalogApi.listPickups()
      .then((p) => {
        const list = p || [];
        setPickups(list);
        const def = list.find((x) => x.is_default) || list[0];
        if (def) setPickupID(def.id);
        setLoading(false);
      })
      .catch((e) => {
        setError((e as { message?: string }).message || "Failed to load pickup locations");
        setLoading(false);
      });
  }, []);

  const subtotal = lines.reduce((s, l) => s + l.price * l.quantity * 100, 0);
  const totalWeight = lines.reduce((s, l) => s + l.weight * l.quantity, 0);

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
    packageL >= 1 && packageW >= 1 && packageH >= 1;

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
      const ship = { line1: shipLine1, city: shipCity, state: shipState, country: "IN", pincode: shipPincode };
      const order = await ordersApi.create({
        channel,
        channel_order_id: channelOrderID || fallbackChannelOrderID.current,
        order_ref: "",
        buyer_name: buyerName,
        buyer_phone: buyerPhone,
        buyer_email: "",
        billing_address: ship,
        shipping_address: ship,
        shipping_pincode: shipPincode,
        shipping_state: shipState,
        payment_method: paymentMethod,
        subtotal_paise: subtotal,
        shipping_paise: 0,
        discount_paise: 0,
        tax_paise: 0,
        total_paise: subtotal,
        cod_amount_paise: paymentMethod === "cod" ? subtotal : 0,
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

  if (pickups.length === 0) {
    return (
      <Card>
        <CardBody className="mx-auto max-w-md py-12 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-accent/10">
            <MapPin className="h-6 w-6 text-accent" />
          </div>
          <h2 className="text-lg font-semibold">Where do we pick this up?</h2>
          <p className="mt-2 text-sm text-muted">
            Before we can book this order, we need an address where the courier will collect
            your shipment. This is usually your home, shop, or wherever your stock lives.
          </p>
          <Button
            className="mt-6"
            onClick={() => router.push("/warehouses/new?next=/orders/new")}
          >
            Add pickup address <Plus className="h-4 w-4" />
          </Button>
        </CardBody>
      </Card>
    );
  }

  return (
    <form onSubmit={submit} className="mx-auto max-w-3xl space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Create order</h1>
        <p className="mt-1 text-sm text-muted">Add buyer details, items, and we'll allocate the best courier.</p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Buyer</CardTitle>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input required value={buyerName} onChange={(e) => setBuyerName(e.target.value)} />
          </Field>
          <Field
            label="Phone"
            error={buyerPhone !== "+91" && !isValidIndianMobile(buyerPhone) ? "10 digits, starts with 6/7/8/9" : undefined}
          >
            <PhoneInput value={buyerPhone} onChange={setBuyerPhone} required />
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Shipping address</CardTitle>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
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
            <Input required value={shipLine1} onChange={(e) => setShipLine1(e.target.value)} />
          </Field>
          <Field label="City">
            <Input required value={shipCity} onChange={(e) => setShipCity(e.target.value)} />
          </Field>
          <Field label="State">
            <Input required value={shipState} onChange={(e) => setShipState(e.target.value)} />
          </Field>
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
          <Field label="Pickup from">
            <select
              value={pickupID}
              onChange={(e) => setPickupID(e.target.value)}
              className="h-10 w-full rounded-md border border-border bg-surface px-3 text-sm"
            >
              {pickups.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label} · {p.pincode}
                </option>
              ))}
            </select>
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Items</CardTitle>
          <CardDescription>What's inside the package. Weight is per item, in grams.</CardDescription>
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
                  <Field label="Unit price (₹)">
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
        <CardBody className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm text-muted">
            <div>
              Items subtotal{" "}
              <span className="font-medium text-text">{paiseToRupees(subtotal)}</span>
            </div>
            <div className="mt-0.5 text-xs">
              {paymentMethod === "cod"
                ? `${paiseToRupees(subtotal)} to collect from buyer (COD)`
                : "Prepaid — no collection at delivery"}
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
