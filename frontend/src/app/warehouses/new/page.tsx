"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { catalogApi } from "@/lib/api";
import { ChevronRight } from "lucide-react";

export default function NewWarehousePage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const router = useRouter();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [next, setNext] = React.useState("/orders/new");

  const [label, setLabel] = React.useState("");
  const [contactName, setContactName] = React.useState("");
  const [contactPhone, setContactPhone] = React.useState("+91");
  const [contactEmail, setContactEmail] = React.useState("");
  const [line1, setLine1] = React.useState("");
  const [line2, setLine2] = React.useState("");
  const [city, setCity] = React.useState("");
  const [state, setState] = React.useState("");
  const [pincode, setPincode] = React.useState("");
  const [gstin, setGstin] = React.useState("");

  // Same useSearchParams-avoidance pattern as /login — keeps the page
  // statically prerenderable when we later flip `output: "export"`.
  React.useEffect(() => {
    const n = new URLSearchParams(window.location.search).get("next");
    if (n && n.startsWith("/")) setNext(n);
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (submitting) return;
    setError(null);
    setSubmitting(true);
    try {
      await catalogApi.createPickup({
        label: label.trim(),
        contact_name: contactName.trim(),
        contact_phone: contactPhone.trim(),
        contact_email: contactEmail.trim() || undefined,
        address: {
          line1: line1.trim(),
          line2: line2.trim() || undefined,
          city: city.trim(),
          state: state.trim(),
          country: "IN",
          pincode: pincode.trim(),
        },
        pincode: pincode.trim(),
        state: state.trim(),
        gstin: gstin.trim().toUpperCase() || undefined,
        active: true,
        is_default: true,
      });
      router.replace(next);
    } catch (e) {
      const m = (e as { message?: string }).message || "Failed to save pickup address";
      setError(m);
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={submit} className="mx-auto max-w-2xl space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Add a pickup address</h1>
        <p className="mt-1 text-sm text-muted">
          This is where the courier will collect your shipments. You can add more addresses later.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Where to pick up</CardTitle>
          <CardDescription>
            Use a name you'll recognise — e.g. "Home", "Bandra warehouse", "Office".
          </CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Nickname">
            <Input
              required
              minLength={2}
              placeholder="Home"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
          </Field>
          <Field label="GSTIN" hint="Optional. Required only if you bill from this address.">
            <Input
              maxLength={15}
              placeholder="29AABCU9603R1ZX"
              value={gstin}
              onChange={(e) => setGstin(e.target.value.toUpperCase())}
            />
          </Field>
          <Field label="Address line 1" hint="Street, locality">
            <Input required value={line1} onChange={(e) => setLine1(e.target.value)} />
          </Field>
          <Field label="Address line 2" hint="Optional">
            <Input value={line2} onChange={(e) => setLine2(e.target.value)} />
          </Field>
          <Field label="City">
            <Input required value={city} onChange={(e) => setCity(e.target.value)} />
          </Field>
          <Field label="State">
            <Input required value={state} onChange={(e) => setState(e.target.value)} />
          </Field>
          <Field label="Pincode" hint="6 digits">
            <Input
              required
              pattern="[1-9][0-9]{5}"
              placeholder="560001"
              value={pincode}
              onChange={(e) => setPincode(e.target.value)}
            />
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Who the courier should call</CardTitle>
          <CardDescription>
            We share this with the courier so they can reach someone at pickup.
          </CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Contact name">
            <Input required value={contactName} onChange={(e) => setContactName(e.target.value)} />
          </Field>
          <Field label="Contact phone">
            <Input
              required
              placeholder="+919999999999"
              value={contactPhone}
              onChange={(e) => setContactPhone(e.target.value)}
            />
          </Field>
          <Field label="Contact email" hint="Optional">
            <Input
              type="email"
              value={contactEmail}
              onChange={(e) => setContactEmail(e.target.value)}
            />
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardBody className="flex items-center justify-between">
          <div className="text-sm text-muted">
            We'll set this as your default pickup address.
          </div>
          <div className="flex items-center gap-2">
            {error && <span className="text-sm text-danger">{error}</span>}
            <Button type="button" variant="ghost" onClick={() => router.back()}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Save and continue <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        </CardBody>
      </Card>
    </form>
  );
}
