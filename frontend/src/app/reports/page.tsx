"use client";
import * as React from "react";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { reportsApi, paiseToRupees, type DashboardSummary } from "@/lib/api";
import { Activity, IndianRupee, Package, AlertTriangle } from "lucide-react";

export default function ReportsPage() {
  return (
    <Shell>
      <Inner />
    </Shell>
  );
}

function Inner() {
  const [data, setData] = React.useState<DashboardSummary | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    reportsApi.dashboard().then(setData).catch((e) =>
      setError((e as { message?: string }).message || "Failed to load reports"),
    );
  }, []);

  if (error) {
    return (
      <Card>
        <CardBody className="py-12 text-center text-sm text-danger">{error}</CardBody>
      </Card>
    );
  }
  if (!data) {
    return <p className="text-sm text-muted">Loading…</p>;
  }

  const totalOrders = Object.values(data.orders_by_state).reduce((s, n) => s + n, 0);
  const maxDay = Math.max(1, ...data.orders_by_day.map((d) => d.count));

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Reports</h1>
        <p className="mt-1 text-sm text-muted">
          At-a-glance snapshot of your orders, payments, and shipping spend.
        </p>
      </header>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Orders today"
          value={data.orders_today.toLocaleString("en-IN")}
          icon={Activity}
        />
        <StatCard
          label="Orders this week"
          value={data.orders_this_week.toLocaleString("en-IN")}
          icon={Package}
        />
        <StatCard
          label="Shipping spend"
          value={paiseToRupees(data.shipping_spend_paise)}
          icon={IndianRupee}
          hint="lifetime"
        />
        <StatCard
          label="Unpaid prepaid"
          value={data.unpaid_prepaid_count.toLocaleString("en-IN")}
          icon={AlertTriangle}
          tone={data.unpaid_prepaid_count > 0 ? "warning" : "default"}
          hint="orders awaiting payment confirmation"
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Orders by state</CardTitle>
          <CardDescription>{totalOrders.toLocaleString("en-IN")} orders total</CardDescription>
        </CardHeader>
        <CardBody>
          {totalOrders === 0 ? (
            <p className="text-sm text-muted">No orders yet — create one to see this populate.</p>
          ) : (
            <ul className="grid gap-2 sm:grid-cols-2 md:grid-cols-3">
              {Object.entries(data.orders_by_state)
                .sort((a, b) => b[1] - a[1])
                .map(([state, n]) => {
                  const pct = Math.round((n / totalOrders) * 100);
                  return (
                    <li key={state} className="rounded-md border border-border bg-bg/30 p-3">
                      <div className="flex items-baseline justify-between">
                        <span className="text-sm font-medium capitalize">{state.replace("_", " ")}</span>
                        <span className="text-sm tabular-nums">{n.toLocaleString("en-IN")}</span>
                      </div>
                      <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-bg">
                        <div
                          className="h-full rounded-full bg-accent/70"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                      <div className="mt-1 text-[11px] text-muted">{pct}%</div>
                    </li>
                  );
                })}
            </ul>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Orders, last 7 days</CardTitle>
          <CardDescription>
            Bars are scaled to the busiest day in the window. India timezone.
          </CardDescription>
        </CardHeader>
        <CardBody>
          <div className="flex items-end gap-3">
            {data.orders_by_day.map((d) => {
              const h = Math.round((d.count / maxDay) * 100);
              const label = new Date(d.day).toLocaleDateString("en-IN", {
                weekday: "short",
              });
              return (
                <div key={d.day} className="flex flex-1 flex-col items-center gap-1">
                  <div className="flex h-32 w-full items-end">
                    <div
                      className={
                        "w-full rounded-t " +
                        (d.count > 0 ? "bg-accent/70" : "bg-border")
                      }
                      style={{ height: `${Math.max(4, h)}%` }}
                      title={`${d.day}: ${d.count}`}
                    />
                  </div>
                  <div className="text-[11px] text-muted">{label}</div>
                  <div className="text-[11px] font-medium tabular-nums">{d.count}</div>
                </div>
              );
            })}
          </div>
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>COD outstanding</CardTitle>
          <CardDescription>
            Total COD value on shipments that are booked or in transit but not
            yet delivered (and therefore not yet collected from the buyer).
          </CardDescription>
        </CardHeader>
        <CardBody>
          <div className="text-3xl font-semibold tabular-nums">
            {paiseToRupees(data.cod_outstanding_paise)}
          </div>
        </CardBody>
      </Card>
    </div>
  );
}

function StatCard({
  label,
  value,
  icon: Icon,
  hint,
  tone = "default",
}: {
  label: string;
  value: string;
  icon: React.ComponentType<{ className?: string }>;
  hint?: string;
  tone?: "default" | "warning";
}) {
  return (
    <Card>
      <CardBody>
        <div className="flex items-start justify-between gap-2">
          <div>
            <div className="text-xs uppercase tracking-wider text-muted">{label}</div>
            <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
            {hint && <div className="mt-0.5 text-[11px] text-muted">{hint}</div>}
          </div>
          <Icon className={"h-5 w-5 " + (tone === "warning" ? "text-warning" : "text-accent")} />
        </div>
      </CardBody>
    </Card>
  );
}
