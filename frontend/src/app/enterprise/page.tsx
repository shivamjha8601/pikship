"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription, CardFooter } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Badge";
import { sellers, paiseToRupees, type Seller, type Contract, type Usage } from "@/lib/api";
import { Building2, Check, Sparkles } from "lucide-react";

export default function EnterprisePage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const router = useRouter();
  const [seller, setSeller] = React.useState<Seller | null>(null);
  const [contract, setContract] = React.useState<Contract | null>(null);
  const [usage, setUsage] = React.useState<Usage | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  const load = React.useCallback(() => {
    Promise.all([
      sellers.get(),
      sellers.contract().catch(() => null),
      sellers.usage().catch(() => null),
    ]).then(([s, c, u]) => {
      setSeller(s);
      setContract(c);
      setUsage(u);
      setLoading(false);
    }).catch(() => setLoading(false));
  }, []);

  React.useEffect(() => { load(); }, [load]);

  const isEnterprise = seller?.seller_type === "enterprise";

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Enterprise</h1>
        <p className="mt-1 text-sm text-muted">Plan, contract terms, and account configuration.</p>
      </header>

      {loading ? (
        <Card><CardBody className="py-8 text-center text-sm text-muted">Loading…</CardBody></Card>
      ) : (
        <>
          {/* Plan card */}
          <Card>
            <CardBody className="flex flex-wrap items-center justify-between gap-4">
              <div className="flex items-center gap-4">
                <div className={"flex h-12 w-12 items-center justify-center rounded-lg " + (isEnterprise ? "bg-accent/10 text-accent" : "bg-bg text-muted")}>
                  <Building2 className="h-6 w-6" />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <span className="text-lg font-semibold capitalize">
                      {seller?.seller_type.replace("_", " ")} plan
                    </span>
                    {isEnterprise && <Badge tone="accent"><Sparkles className="h-3 w-3 mr-1 inline-block" />Enterprise</Badge>}
                  </div>
                  <p className="text-sm text-muted">
                    {isEnterprise ? "Custom contract active" : "Standard self-serve plan"}
                  </p>
                </div>
              </div>
              {!isEnterprise && (
                <UpgradeButton
                  sellerID={seller?.id || ""}
                  onUpgraded={() => load()}
                  onError={setError}
                />
              )}
            </CardBody>
          </Card>

          {error && (
            <div className="rounded-md border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
              {error}
            </div>
          )}

          {/* Active contract */}
          {contract ? (
            <Card>
              <CardHeader>
                <CardTitle>Active contract — v{contract.version}</CardTitle>
                <CardDescription>
                  Effective from {new Date(contract.effective_from).toLocaleDateString()}
                  {" · "}
                  Status: <span className="font-medium">{contract.state}</span>
                </CardDescription>
              </CardHeader>
              <CardBody className="grid gap-6 lg:grid-cols-2">
                <ContractTerms contract={contract} />
                <CapacitySnapshot usage={usage} />
              </CardBody>
              <CardFooter className="flex justify-end">
                <Button variant="ghost" onClick={() => router.push("/orders")}>View orders</Button>
              </CardFooter>
            </Card>
          ) : (
            <Card>
              <CardBody className="py-12 text-center">
                <p className="text-sm font-medium">No active contract</p>
                <p className="mt-1 text-xs text-muted">
                  Standard plans use platform defaults. Upgrade to enterprise to negotiate terms.
                </p>
              </CardBody>
            </Card>
          )}

          {/* Plan comparison */}
          {!isEnterprise && (
            <PlanComparison />
          )}
        </>
      )}
    </div>
  );
}

function UpgradeButton({ sellerID, onUpgraded, onError }: {
  sellerID: string;
  onUpgraded: () => void;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = React.useState(false);

  async function upgrade() {
    if (!confirm("Upgrade to enterprise? This activates a custom contract with unlimited orders, ₹5L credit, and insurance.")) return;
    setBusy(true);
    try {
      await sellers.upgrade(sellerID, {
        new_type: "enterprise",
        terms: {
          policy_overrides: {
            "limits.orders_per_day": 0,
            "limits.shipments_per_month": 0,
            "features.insurance": true,
            "wallet.credit_limit_inr": 50_000_000,
            "features.weight_dispute_auto": true,
          },
          monthly_minimum_paise: 100_000_000,
          sla_delivered_p95_days: 3,
        },
      });
      onUpgraded();
    } catch (e) {
      onError((e as { message?: string }).message || "Upgrade failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Button onClick={upgrade} loading={busy}>
      <Sparkles className="h-4 w-4" /> Upgrade to enterprise
    </Button>
  );
}

function ContractTerms({ contract }: { contract: Contract }) {
  const overrides = (contract.terms?.policy_overrides as Record<string, unknown>) || {};
  return (
    <div>
      <h4 className="mb-3 text-sm font-semibold">Active terms</h4>
      <ul className="space-y-2 text-sm">
        {Object.entries(overrides).map(([k, v]) => (
          <li key={k} className="flex justify-between gap-4 border-b border-border pb-2">
            <span className="text-muted">{prettyKey(k)}</span>
            <span className="font-medium text-text">{prettyValue(k, v)}</span>
          </li>
        ))}
        {typeof contract.terms?.sla_delivered_p95_days === "number" && (
          <li className="flex justify-between gap-4 border-b border-border pb-2">
            <span className="text-muted">Delivered P95 SLA</span>
            <span className="font-medium">{contract.terms.sla_delivered_p95_days} days</span>
          </li>
        )}
        {typeof contract.terms?.monthly_minimum_paise === "number" && (
          <li className="flex justify-between gap-4 border-b border-border pb-2">
            <span className="text-muted">Monthly minimum</span>
            <span className="font-medium">
              {paiseToRupees(contract.terms.monthly_minimum_paise)}
            </span>
          </li>
        )}
      </ul>
    </div>
  );
}

function CapacitySnapshot({ usage }: { usage: Usage | null }) {
  if (!usage) return null;
  return (
    <div>
      <h4 className="mb-3 text-sm font-semibold">Capacity</h4>
      <div className="space-y-3 text-sm">
        <Cap label="Orders today" count={usage.orders_today} limit={usage.order_day_limit} />
        <Cap label="Shipments this month" count={usage.shipments_this_month} limit={usage.shipment_month_limit} />
      </div>
    </div>
  );
}

function Cap({ label, count, limit }: { label: string; count: number; limit: number }) {
  const unlimited = limit === 0;
  const pct = unlimited ? 100 : Math.min(100, (count / limit) * 100);
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span>{label}</span>
        <span className="font-medium">{count.toLocaleString()}{unlimited ? "" : ` / ${limit.toLocaleString()}`}</span>
      </div>
      <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-bg">
        <div className={"h-full " + (unlimited ? "bg-accent/40" : pct > 80 ? "bg-warning" : "bg-success")} style={{ width: pct + "%" }} />
      </div>
      {unlimited && <div className="mt-1 text-xs text-muted">Unlimited</div>}
    </div>
  );
}

function PlanComparison() {
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Standard</CardTitle>
          <CardDescription>Self-serve, instant signup.</CardDescription>
        </CardHeader>
        <CardBody>
          <ul className="space-y-2 text-sm">
            <Bullet>200 orders / day</Bullet>
            <Bullet>500 shipments / month</Bullet>
            <Bullet>4 carriers (Delhivery, DTDC, Ekart, Ecom Express)</Bullet>
            <Bullet>Standard rate cards</Bullet>
          </ul>
        </CardBody>
      </Card>
      <Card className="border-accent/40 ring-1 ring-accent/20">
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>Enterprise</CardTitle>
            <Badge tone="accent">Recommended</Badge>
          </div>
          <CardDescription>Custom contract, dedicated rates, SLA.</CardDescription>
        </CardHeader>
        <CardBody>
          <ul className="space-y-2 text-sm">
            <Bullet>Unlimited orders & shipments</Bullet>
            <Bullet>All 8 carriers + premium services</Bullet>
            <Bullet>Custom rate cards</Bullet>
            <Bullet>₹5,00,000 credit limit</Bullet>
            <Bullet>Auto weight-dispute filing</Bullet>
            <Bullet>Insurance attach</Bullet>
            <Bullet>P95 delivery SLA: 3 days</Bullet>
          </ul>
        </CardBody>
      </Card>
    </div>
  );
}

function Bullet({ children }: { children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-2">
      <Check className="h-4 w-4 flex-shrink-0 text-success mt-0.5" />
      <span>{children}</span>
    </li>
  );
}

function prettyKey(k: string): string {
  const map: Record<string, string> = {
    "limits.orders_per_day": "Orders / day",
    "limits.shipments_per_month": "Shipments / month",
    "features.insurance": "Insurance",
    "features.weight_dispute_auto": "Auto weight-dispute filing",
    "wallet.credit_limit_inr": "Credit limit",
  };
  return map[k] || k;
}

function prettyValue(k: string, v: unknown): string {
  if (k === "wallet.credit_limit_inr" && typeof v === "number") return paiseToRupees(v);
  if (typeof v === "boolean") return v ? "Enabled" : "Disabled";
  if (k.startsWith("limits.") && v === 0) return "Unlimited";
  return String(v);
}

