"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PhoneInput, isValidIndianMobile } from "@/components/ui/PhoneInput";
import { PincodeInput } from "@/components/ui/PincodeInput";
import { catalogApi, ordersApi, type PickupLocation } from "@/lib/api";
import { MapPin, Plus, Trash2 } from "lucide-react";

type Line = { sku: string; name: string; quantity: number; price: number; weight: number };

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
    lines.every((l) => l.quantity >= 1 && l.weight >= 1);

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
        package_length_mm: 200,
        package_width_mm: 150,
        package_height_mm: 100,
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
        </CardHeader>
        <CardBody className="space-y-3">
          {lines.map((l, i) => (
            <div key={i} className="grid grid-cols-12 gap-2">
              <Input
                placeholder="SKU"
                className="col-span-2"
                value={l.sku}
                onChange={(e) => setLine(i, { sku: e.target.value })}
              />
              <Input
                placeholder="Name"
                className="col-span-4"
                value={l.name}
                onChange={(e) => setLine(i, { name: e.target.value })}
              />
              <Input
                type="number"
                placeholder="Qty"
                min={1}
                className="col-span-2"
                value={l.quantity}
                onChange={(e) => setLine(i, { quantity: Number(e.target.value) })}
              />
              <Input
                type="number"
                placeholder="Price ₹"
                min={0}
                className="col-span-2"
                value={l.price}
                onChange={(e) => setLine(i, { price: Number(e.target.value) })}
              />
              <Input
                type="number"
                placeholder="Weight g"
                min={0}
                className="col-span-1"
                value={l.weight}
                onChange={(e) => setLine(i, { weight: Number(e.target.value) })}
              />
              <Button
                type="button"
                variant="ghost"
                size="md"
                className="col-span-1"
                onClick={() => removeLine(i)}
                disabled={lines.length === 1}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
          <Button type="button" variant="ghost" onClick={addLine}>
            <Plus className="h-4 w-4" /> Add item
          </Button>
        </CardBody>
      </Card>

      <Card>
        <CardBody className="flex items-center justify-between">
          <div className="text-sm text-muted">
            Subtotal: <span className="font-medium text-text">₹{(subtotal / 100).toFixed(2)}</span>
            {" · "}
            Weight: <span className="font-medium text-text">{totalWeight}g</span>
          </div>
          <div className="flex items-center gap-2">
            {error && <span className="text-sm text-danger">{error}</span>}
            <Button type="submit" loading={submitting} disabled={!canSubmit}>Create order</Button>
          </div>
        </CardBody>
      </Card>
    </form>
  );
}
