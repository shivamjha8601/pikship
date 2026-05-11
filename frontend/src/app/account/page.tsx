"use client";
import * as React from "react";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { Badge } from "@/components/ui/Badge";
import { useSession } from "@/lib/session";
import {
  catalogApi,
  sellers as sellersApi,
  type BuyerAddress,
  type PickupLocation,
  type Seller,
} from "@/lib/api";
import {
  AddressForm,
  EMPTY_ADDRESS,
  type AddressFormValue,
} from "@/components/account/AddressForm";
import {
  WarehouseForm,
  EMPTY_WAREHOUSE,
  type WarehouseFormValue,
} from "@/components/account/WarehouseForm";
import { cn } from "@/lib/cn";
import {
  User as UserIcon,
  MapPin,
  Warehouse,
  ShieldCheck,
  Star,
  Trash2,
  Pencil,
  Plus,
} from "lucide-react";

type TabId = "profile" | "addresses" | "warehouses" | "kyc";

const TABS: { id: TabId; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
  { id: "profile", label: "Profile", icon: UserIcon },
  { id: "addresses", label: "Addresses", icon: MapPin },
  { id: "warehouses", label: "Warehouses", icon: Warehouse },
  { id: "kyc", label: "KYC", icon: ShieldCheck },
];

export default function AccountPage() {
  return (
    <Shell>
      <Inner />
    </Shell>
  );
}

function Inner() {
  const [tab, setTab] = React.useState<TabId>("profile");

  // Read ?tab=… on mount so deep links work. We avoid useSearchParams to keep
  // the page statically prerenderable, matching the pattern used elsewhere.
  React.useEffect(() => {
    const q = new URLSearchParams(window.location.search).get("tab");
    if (q && TABS.some((t) => t.id === q)) setTab(q as TabId);
  }, []);

  React.useEffect(() => {
    const url = new URL(window.location.href);
    url.searchParams.set("tab", tab);
    window.history.replaceState({}, "", url.toString());
  }, [tab]);

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Account</h1>
        <p className="mt-1 text-sm text-muted">
          Manage your profile, saved addresses, pickup warehouses, and KYC.
        </p>
      </header>

      <nav
        role="tablist"
        aria-label="Account sections"
        className="flex flex-wrap gap-1 rounded-lg border border-border bg-surface p-1"
      >
        {TABS.map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={cn(
              "inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium",
              tab === id
                ? "bg-accent/10 text-accent"
                : "text-muted hover:bg-bg hover:text-text",
            )}
          >
            <Icon className="h-4 w-4" />
            {label}
          </button>
        ))}
      </nav>

      {tab === "profile" && <ProfileTab />}
      {tab === "addresses" && <AddressesTab />}
      {tab === "warehouses" && <WarehousesTab />}
      {tab === "kyc" && <KYCTab />}
    </div>
  );
}

// ─── Profile ──────────────────────────────────────────────────────────────

function ProfileTab() {
  const { user, sellers, logout } = useSession();
  const [seller, setSeller] = React.useState<Seller | null>(null);
  const [err, setErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    sellersApi.get().then(setSeller).catch((e: { message?: string }) =>
      setErr(e.message || "Failed to load seller"),
    );
  }, []);

  if (!user) return null;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Profile</CardTitle>
          <CardDescription>Your account on Pikshipp.</CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input value={user.name || ""} readOnly />
          </Field>
          <Field label="Email">
            <Input value={user.email} readOnly />
          </Field>
          <Field label="Status">
            <Input value={user.status} readOnly />
          </Field>
          <Field label="Member since">
            <Input value={new Date(user.created_at).toLocaleDateString()} readOnly />
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Seller</CardTitle>
          <CardDescription>
            The business you're operating as. Memberships: {sellers.length}.
          </CardDescription>
        </CardHeader>
        <CardBody className="grid gap-4 sm:grid-cols-2">
          {err && <p className="text-sm text-danger sm:col-span-2">{err}</p>}
          <Field label="Legal name">
            <Input value={seller?.legal_name || ""} readOnly />
          </Field>
          <Field label="Display name">
            <Input value={seller?.display_name || ""} readOnly />
          </Field>
          <Field label="Type">
            <Input value={seller?.seller_type || ""} readOnly />
          </Field>
          <Field label="Lifecycle">
            <Input value={seller?.lifecycle_state || ""} readOnly />
          </Field>
          <Field label="GSTIN">
            <Input value={seller?.gstin || ""} readOnly />
          </Field>
          <Field label="PAN">
            <Input value={seller?.pan || ""} readOnly />
          </Field>
          <Field label="Primary phone">
            <Input value={seller?.primary_phone || ""} readOnly />
          </Field>
          <Field label="Billing email">
            <Input value={seller?.billing_email || ""} readOnly />
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardBody className="flex items-center justify-between">
          <div className="text-sm text-muted">Sign out of this device.</div>
          <Button variant="danger" onClick={logout}>
            Sign out
          </Button>
        </CardBody>
      </Card>
    </div>
  );
}

// ─── Addresses ────────────────────────────────────────────────────────────

function AddressesTab() {
  const [items, setItems] = React.useState<BuyerAddress[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  const [adding, setAdding] = React.useState(false);
  const [editing, setEditing] = React.useState<BuyerAddress | null>(null);
  const [form, setForm] = React.useState<AddressFormValue>(EMPTY_ADDRESS);
  const [submitting, setSubmitting] = React.useState(false);

  const refresh = React.useCallback(async () => {
    try {
      const list = await catalogApi.listBuyerAddresses();
      setItems(list);
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to load addresses");
      setItems([]);
    }
  }, []);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  function openCreate() {
    setForm(EMPTY_ADDRESS);
    setEditing(null);
    setAdding(true);
  }

  function openEdit(a: BuyerAddress) {
    setForm({
      label: a.label,
      buyer_name: a.buyer_name,
      buyer_phone: a.buyer_phone,
      buyer_email: a.buyer_email || "",
      address: { ...a.address, country: a.address.country || "IN" },
      pincode: a.pincode,
      state: a.state,
      is_default: a.is_default,
    });
    setEditing(a);
    setAdding(true);
  }

  async function save() {
    setSubmitting(true);
    setErr(null);
    try {
      if (editing) {
        await catalogApi.updateBuyerAddress(editing.id, form);
        if (form.is_default && !editing.is_default) {
          await catalogApi.setDefaultBuyerAddress(editing.id);
        }
      } else {
        await catalogApi.createBuyerAddress(form);
      }
      setAdding(false);
      setEditing(null);
      await refresh();
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to save address");
    } finally {
      setSubmitting(false);
    }
  }

  async function remove(a: BuyerAddress) {
    if (!confirm(`Delete "${a.label}"? Past orders keep their copy of this address.`)) return;
    try {
      await catalogApi.deleteBuyerAddress(a.id);
      await refresh();
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to delete");
    }
  }

  async function makeDefault(a: BuyerAddress) {
    try {
      await catalogApi.setDefaultBuyerAddress(a.id);
      await refresh();
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to set default");
    }
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex items-center justify-between">
          <div>
            <CardTitle>Saved ship-to addresses</CardTitle>
            <CardDescription>
              Pick from these when creating orders so you don't re-type the same address.
            </CardDescription>
          </div>
          {!adding && (
            <Button size="sm" onClick={openCreate}>
              <Plus className="h-4 w-4" /> Add
            </Button>
          )}
        </CardHeader>
        <CardBody>
          {err && <p className="mb-3 text-sm text-danger">{err}</p>}
          {adding && (
            <div className="mb-4 rounded-md border border-border p-4">
              <h3 className="mb-3 text-sm font-medium">
                {editing ? `Edit "${editing.label}"` : "New address"}
              </h3>
              <AddressForm
                value={form}
                onChange={setForm}
                onSubmit={save}
                onCancel={() => {
                  setAdding(false);
                  setEditing(null);
                }}
                submitLabel={editing ? "Save changes" : "Add address"}
                submitting={submitting}
              />
            </div>
          )}

          {items === null ? (
            <p className="text-sm text-muted">Loading…</p>
          ) : items.length === 0 ? (
            <p className="text-sm text-muted">
              No saved addresses yet. Click <strong>Add</strong> to save one for reuse.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {items.map((a) => (
                <li key={a.id} className="flex items-start justify-between gap-4 py-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{a.label}</span>
                      {a.is_default && (
                        <Badge tone="accent">
                          <Star className="h-3 w-3" /> Default
                        </Badge>
                      )}
                    </div>
                    <div className="mt-0.5 text-sm">
                      {a.buyer_name} · {a.buyer_phone}
                    </div>
                    <div className="text-sm text-muted">
                      {a.address.line1}
                      {a.address.line2 ? `, ${a.address.line2}` : ""}, {a.address.city}, {a.state} {a.pincode}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    {!a.is_default && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => makeDefault(a)}
                        aria-label="Make default"
                      >
                        <Star className="h-4 w-4" />
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => openEdit(a)}
                      aria-label="Edit"
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => remove(a)}
                      aria-label="Delete"
                    >
                      <Trash2 className="h-4 w-4 text-danger" />
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardBody>
      </Card>
    </div>
  );
}

// ─── Warehouses ───────────────────────────────────────────────────────────

function WarehousesTab() {
  const [items, setItems] = React.useState<PickupLocation[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  const [adding, setAdding] = React.useState(false);
  const [form, setForm] = React.useState<WarehouseFormValue>(EMPTY_WAREHOUSE);
  const [submitting, setSubmitting] = React.useState(false);

  const refresh = React.useCallback(async () => {
    try {
      const list = await catalogApi.listPickups();
      setItems(list ?? []);
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to load");
      setItems([]);
    }
  }, []);

  React.useEffect(() => {
    refresh();
  }, [refresh]);

  async function save() {
    setSubmitting(true);
    setErr(null);
    try {
      await catalogApi.createPickup(form);
      setAdding(false);
      setForm(EMPTY_WAREHOUSE);
      await refresh();
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to save warehouse");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex items-center justify-between">
        <div>
          <CardTitle>Pickup warehouses</CardTitle>
          <CardDescription>
            Where couriers collect your shipments. Pick one when creating an order.
          </CardDescription>
        </div>
        {!adding && (
          <Button
            size="sm"
            onClick={() => {
              setForm(EMPTY_WAREHOUSE);
              setAdding(true);
            }}
          >
            <Plus className="h-4 w-4" /> Add warehouse
          </Button>
        )}
      </CardHeader>
      <CardBody>
        {err && <p className="mb-3 text-sm text-danger">{err}</p>}
        {adding && (
          <div className="mb-4 rounded-md border border-border p-4">
            <h3 className="mb-3 text-sm font-medium">New warehouse</h3>
            <WarehouseForm
              value={form}
              onChange={setForm}
              onSubmit={save}
              onCancel={() => setAdding(false)}
              submitLabel="Add warehouse"
              submitting={submitting}
            />
          </div>
        )}
        {items === null ? (
          <p className="text-sm text-muted">Loading…</p>
        ) : items.length === 0 ? (
          <p className="text-sm text-muted">
            No pickup warehouses yet. Click <strong>Add warehouse</strong> to create one.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            {items.map((w) => (
              <li key={w.id} className="flex items-start justify-between gap-4 py-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{w.label}</span>
                    {w.is_default && (
                      <Badge tone="accent">
                        <Star className="h-3 w-3" /> Default
                      </Badge>
                    )}
                    {!w.active && <Badge tone="warning">Inactive</Badge>}
                  </div>
                  <div className="mt-0.5 text-sm">
                    {w.contact_name} · {w.contact_phone}
                  </div>
                  <div className="text-sm text-muted">
                    {w.address.line1}, {w.address.city}, {w.state} {w.pincode}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardBody>
    </Card>
  );
}

// ─── KYC ──────────────────────────────────────────────────────────────────

function KYCTab() {
  const [seller, setSeller] = React.useState<Seller | null>(null);
  const [legalName, setLegalName] = React.useState("");
  const [gstin, setGstin] = React.useState("");
  const [pan, setPan] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [ok, setOk] = React.useState(false);

  React.useEffect(() => {
    sellersApi.get().then((s) => {
      setSeller(s);
      setLegalName(s.legal_name || "");
      setGstin(s.gstin || "");
      setPan(s.pan || "");
    }).catch((e: { message?: string }) => setErr(e.message || "Failed to load seller"));
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr(null);
    setOk(false);
    setSubmitting(true);
    try {
      await sellersApi.submitKYC({ legal_name: legalName.trim(), gstin: gstin.trim().toUpperCase(), pan: pan.trim().toUpperCase() });
      setOk(true);
      const s = await sellersApi.get();
      setSeller(s);
    } catch (e) {
      setErr((e as { message?: string }).message || "Failed to submit");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>KYC</CardTitle>
        <CardDescription>
          Legal name, GSTIN, and PAN. Required for shipment booking on live carriers.
        </CardDescription>
      </CardHeader>
      <CardBody>
        <div className="mb-3 text-sm">
          Current state:{" "}
          <Badge tone={seller?.lifecycle_state === "active" ? "success" : "warning"}>
            {seller?.lifecycle_state || "loading"}
          </Badge>
        </div>
        <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2">
          <Field label="Legal name">
            <Input required value={legalName} onChange={(e) => setLegalName(e.target.value)} />
          </Field>
          <Field label="GSTIN" hint="15 chars, e.g. 29AABCU9603R1ZX">
            <Input
              required
              maxLength={15}
              value={gstin}
              onChange={(e) => setGstin(e.target.value.toUpperCase())}
            />
          </Field>
          <Field label="PAN" hint="10 chars, e.g. AAAPL1234C">
            <Input
              required
              maxLength={10}
              value={pan}
              onChange={(e) => setPan(e.target.value.toUpperCase())}
            />
          </Field>
          <div className="flex items-end justify-end sm:col-span-2">
            {err && <span className="mr-3 text-sm text-danger">{err}</span>}
            {ok && <span className="mr-3 text-sm text-success">Submitted.</span>}
            <Button type="submit" loading={submitting}>
              Submit KYC
            </Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}
