"use client";
import * as React from "react";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PincodeInput } from "@/components/ui/PincodeInput";
import { Badge } from "@/components/ui/Badge";
import {
  pricingApi,
  paiseToRupees,
  type PricingQuote,
  type PricingPackage,
} from "@/lib/api";
import { Calculator, Package as PackageIcon, Plus, Trash2 } from "lucide-react";

type Pkg = { weightG: number; lengthCm: number; widthCm: number; heightCm: number };

const EMPTY_PKG: Pkg = { weightG: 500, lengthCm: 20, widthCm: 15, heightCm: 10 };

export default function PricingPage() {
  return (
    <Shell>
      <Inner />
    </Shell>
  );
}

function Inner() {
  const [pickup, setPickup] = React.useState("");
  const [shipTo, setShipTo] = React.useState("");
  const [paymentMode, setPaymentMode] = React.useState<"prepaid" | "cod">("prepaid");
  const [pkgs, setPkgs] = React.useState<Pkg[]>([EMPTY_PKG]);
  const [loading, setLoading] = React.useState(false);
  const [quotes, setQuotes] = React.useState<PricingQuote[] | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  const canQuote =
    /^[1-9]\d{5}$/.test(pickup) &&
    /^[1-9]\d{5}$/.test(shipTo) &&
    pkgs.every((p) => p.weightG > 0 && p.lengthCm > 0 && p.widthCm > 0 && p.heightCm > 0);

  function setPkg(i: number, patch: Partial<Pkg>) {
    setPkgs((cur) => cur.map((p, idx) => (idx === i ? { ...p, ...patch } : p)));
  }

  async function quote() {
    if (!canQuote) return;
    setLoading(true);
    setError(null);
    setQuotes(null);
    try {
      const payload: PricingPackage[] = pkgs.map((p) => ({
        weight_g: p.weightG,
        length_mm: p.lengthCm * 10,
        width_mm: p.widthCm * 10,
        height_mm: p.heightCm * 10,
      }));
      const res = await pricingApi.quote({
        pickup_pincode: pickup,
        ship_to_pincode: shipTo,
        payment_mode: paymentMode,
        packages: payload,
      });
      const sorted = [...(res.quotes || [])].sort((a, b) => a.total_paise - b.total_paise);
      setQuotes(sorted);
    } catch (e) {
      setError((e as { message?: string }).message || "Failed to get quote");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <header>
        <h1 className="flex items-center gap-2 text-2xl font-semibold">
          <Calculator className="h-6 w-6 text-accent" />
          Pricing calculator
        </h1>
        <p className="mt-1 text-sm text-muted">
          Enter pickup + delivery pincodes and the parcel dimensions. We'll
          quote every carrier we have rates for, ranked by price.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Route</CardTitle>
          <CardDescription>Pincodes determine the zone we price into.</CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Pickup pincode">
            <PincodeInput value={pickup} onChange={setPickup} required />
          </Field>
          <Field label="Ship-to pincode">
            <PincodeInput value={shipTo} onChange={setShipTo} required />
          </Field>
          <Field label="Payment mode">
            <select
              value={paymentMode}
              onChange={(e) => setPaymentMode(e.target.value as "prepaid" | "cod")}
              className="h-10 w-full rounded-md border border-border bg-surface px-3 text-sm"
            >
              <option value="prepaid">Prepaid</option>
              <option value="cod">Cash on Delivery</option>
            </select>
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader className="flex items-center justify-between">
          <div>
            <CardTitle className="flex items-center gap-2">
              <PackageIcon className="h-4 w-4 text-muted" /> Packages
            </CardTitle>
            <CardDescription>
              One row per box. Price = base + volumetric weight ((L×W×H)/5000g)
              vs declared weight, whichever is higher.
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setPkgs((cur) => [...cur, EMPTY_PKG])}
          >
            <Plus className="h-4 w-4" /> Add package
          </Button>
        </CardHeader>
        <CardBody className="space-y-3">
          {pkgs.map((p, i) => (
            <div
              key={i}
              className="rounded-lg border border-border bg-bg/30 p-4"
            >
              <div className="mb-3 flex items-center justify-between">
                <span className="text-xs font-medium uppercase tracking-wider text-muted">
                  Package {i + 1}
                </span>
                {pkgs.length > 1 && (
                  <button
                    type="button"
                    onClick={() => setPkgs((cur) => cur.filter((_, idx) => idx !== i))}
                    className="rounded-md p-1 text-muted hover:bg-danger/10 hover:text-danger"
                    aria-label={`Remove package ${i + 1}`}
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                )}
              </div>
              <div className="grid grid-cols-4 gap-3">
                <Field label="Weight (g)">
                  <Input
                    type="number"
                    className="no-spin"
                    min={1}
                    value={p.weightG}
                    onChange={(e) => setPkg(i, { weightG: Math.max(1, Number(e.target.value) || 0) })}
                  />
                </Field>
                <Field label="L (cm)">
                  <Input
                    type="number"
                    className="no-spin"
                    min={1}
                    value={p.lengthCm}
                    onChange={(e) => setPkg(i, { lengthCm: Math.max(1, Number(e.target.value) || 0) })}
                  />
                </Field>
                <Field label="W (cm)">
                  <Input
                    type="number"
                    className="no-spin"
                    min={1}
                    value={p.widthCm}
                    onChange={(e) => setPkg(i, { widthCm: Math.max(1, Number(e.target.value) || 0) })}
                  />
                </Field>
                <Field label="H (cm)">
                  <Input
                    type="number"
                    className="no-spin"
                    min={1}
                    value={p.heightCm}
                    onChange={(e) => setPkg(i, { heightCm: Math.max(1, Number(e.target.value) || 0) })}
                  />
                </Field>
              </div>
            </div>
          ))}
        </CardBody>
      </Card>

      <Card>
        <CardBody className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm text-muted">
            {pkgs.length} package{pkgs.length === 1 ? "" : "s"} · {pkgs.reduce((s, p) => s + p.weightG, 0).toLocaleString()} g
          </div>
          <Button onClick={quote} disabled={!canQuote} loading={loading}>
            Get quote
          </Button>
        </CardBody>
      </Card>

      {error && (
        <div className="rounded-md border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
          {error}
        </div>
      )}

      {quotes && (
        <Card>
          <CardHeader>
            <CardTitle>Quotes</CardTitle>
            <CardDescription>
              Sorted by price. Lower-cost first; estimated delivery is the slowest
              package in the route.
            </CardDescription>
          </CardHeader>
          <CardBody>
            {quotes.length === 0 ? (
              <p className="text-sm text-muted">
                No carriers can price this route. Try a different pincode pair or
                contact support.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {quotes.map((q, idx) => (
                  <li
                    key={`${q.carrier_id}-${q.service_type}`}
                    className="flex items-center justify-between gap-4 py-3"
                  >
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="font-medium capitalize">{q.carrier_code || "carrier"}</span>
                        <Badge tone="neutral">{q.service_type}</Badge>
                        {idx === 0 && <Badge tone="success">Cheapest</Badge>}
                        {q.zone && <Badge tone="accent">Zone {q.zone}</Badge>}
                      </div>
                      <div className="mt-0.5 text-xs text-muted">
                        {q.estimated_days} day{q.estimated_days === 1 ? "" : "s"} estimated
                        {q.packages > 1 && ` · ${q.packages} packages`}
                      </div>
                    </div>
                    <div className="text-right">
                      <div className="text-lg font-semibold tabular-nums">
                        {paiseToRupees(q.total_paise)}
                      </div>
                      <div className="text-xs text-muted">incl. fuel & handling</div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardBody>
        </Card>
      )}
    </div>
  );
}
