"use client";
import * as React from "react";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { ordersApi, sellers, walletApi, paiseToRupees, type Order, type Usage, type WalletBalance, type Seller } from "@/lib/api";
import { OrderStateBadge } from "@/components/OrderStateBadge";
import { Package, TrendingUp, AlertTriangle, Wallet as WalletIcon } from "lucide-react";

export default function DashboardPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const [orders, setOrders] = React.useState<Order[]>([]);
  const [usage, setUsage] = React.useState<Usage | null>(null);
  const [balance, setBalance] = React.useState<WalletBalance | null>(null);
  const [seller, setSeller] = React.useState<Seller | null>(null);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    Promise.all([
      ordersApi.list().then(r => r.orders).catch(() => []),
      sellers.usage().catch(() => null),
      walletApi.balance().catch(() => null),
      sellers.get().catch(() => null),
    ]).then(([o, u, b, s]) => {
      setOrders(o);
      setUsage(u);
      setBalance(b);
      setSeller(s);
      setLoading(false);
    });
  }, []);

  const counts = React.useMemo(() => {
    const c: Record<string, number> = {};
    for (const o of orders) c[o.state] = (c[o.state] || 0) + 1;
    return c;
  }, [orders]);

  return (
    <div className="space-y-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">
            {seller ? `Good day, ${seller.display_name}` : "Dashboard"}
          </h1>
          <p className="mt-1 text-sm text-muted">
            Live snapshot of orders, capacity, and wallet.
          </p>
        </div>
        <div className="flex gap-2">
          <Link href="/orders/new"><Button>Create order</Button></Link>
          <Link href="/orders"><Button variant="secondary">View all orders</Button></Link>
        </div>
      </header>

      {/* Stat cards */}
      <div className="grid gap-4 md:grid-cols-4">
        <Stat
          icon={<Package className="h-5 w-5" />}
          label="Total orders"
          value={orders.length.toString()}
          loading={loading}
        />
        <Stat
          icon={<TrendingUp className="h-5 w-5" />}
          label="Delivered"
          value={(counts.delivered || 0).toString()}
          tone="success"
          loading={loading}
        />
        <Stat
          icon={<AlertTriangle className="h-5 w-5" />}
          label="Cancelled / RTO"
          value={((counts.cancelled || 0) + (counts.rto || 0)).toString()}
          tone="warning"
          loading={loading}
        />
        <Stat
          icon={<WalletIcon className="h-5 w-5" />}
          label="Wallet"
          value={balance ? paiseToRupees(balance.available) : "—"}
          tone="accent"
          loading={loading}
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        {/* Recent orders */}
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Recent orders</CardTitle>
            <CardDescription>Latest orders across all channels.</CardDescription>
          </CardHeader>
          <CardBody className="p-0">
            {loading ? (
              <SkeletonList />
            ) : orders.length === 0 ? (
              <Empty message="No orders yet" hint="Create one to see it here." />
            ) : (
              <ul className="divide-y divide-border">
                {orders.slice(0, 8).map((o) => (
                  <li key={o.id}>
                    <Link
                      href={`/orders/${o.id}`}
                      className="flex items-center justify-between gap-3 px-5 py-3 hover:bg-bg"
                    >
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">
                          {o.channel_order_id} · {o.buyer_name}
                        </div>
                        <div className="mt-0.5 text-xs text-muted">
                          {o.shipping_address.city}, {o.shipping_address.state} · {paiseToRupees(o.total_paise)}
                        </div>
                      </div>
                      <OrderStateBadge state={o.state} />
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </CardBody>
        </Card>

        {/* Usage */}
        <Card>
          <CardHeader>
            <CardTitle>Capacity</CardTitle>
            <CardDescription>Your contract caps</CardDescription>
          </CardHeader>
          <CardBody>
            {usage ? (
              <div className="space-y-4">
                <UsageRow
                  label="Orders today"
                  count={usage.orders_today}
                  limit={usage.order_day_limit}
                />
                <UsageRow
                  label="Shipments this month"
                  count={usage.shipments_this_month}
                  limit={usage.shipment_month_limit}
                />
                <Link href="/enterprise" className="block">
                  <Button variant="secondary" className="w-full">View contract</Button>
                </Link>
              </div>
            ) : (
              <Empty message="Capacity unavailable" />
            )}
          </CardBody>
        </Card>
      </div>
    </div>
  );
}

function Stat({
  icon, label, value, tone = "neutral", loading,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  tone?: "neutral" | "success" | "warning" | "accent";
  loading?: boolean;
}) {
  const toneCls = {
    neutral: "bg-bg text-text",
    success: "bg-success/10 text-success",
    warning: "bg-warning/10 text-warning",
    accent: "bg-accent/10 text-accent",
  }[tone];

  return (
    <Card>
      <CardBody>
        <div className="flex items-center justify-between">
          <span className="text-xs uppercase tracking-wider text-muted">{label}</span>
          <div className={"flex h-8 w-8 items-center justify-center rounded-md " + toneCls}>{icon}</div>
        </div>
        <div className="mt-2 text-2xl font-semibold">
          {loading ? <span className="inline-block h-7 w-16 animate-pulse rounded bg-bg" /> : value}
        </div>
      </CardBody>
    </Card>
  );
}

function UsageRow({ label, count, limit }: { label: string; count: number; limit: number }) {
  const pct = limit === 0 ? 0 : Math.min(100, (count / limit) * 100);
  const isUnlimited = limit === 0;
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <span className="text-sm">{label}</span>
        <span className="text-sm font-medium">
          {count.toLocaleString()} {isUnlimited ? "" : `/ ${limit.toLocaleString()}`}
        </span>
      </div>
      <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-bg">
        <div
          className={"h-full rounded-full " + (isUnlimited ? "bg-accent/30" : pct > 80 ? "bg-warning" : "bg-success")}
          style={{ width: isUnlimited ? "100%" : pct + "%" }}
        />
      </div>
      {isUnlimited && <div className="mt-1 text-xs text-muted">Unlimited (enterprise)</div>}
    </div>
  );
}

function SkeletonList() {
  return (
    <ul className="divide-y divide-border">
      {Array.from({ length: 4 }).map((_, i) => (
        <li key={i} className="flex items-center justify-between px-5 py-3">
          <div className="space-y-2">
            <div className="h-3 w-40 animate-pulse rounded bg-bg" />
            <div className="h-3 w-24 animate-pulse rounded bg-bg" />
          </div>
          <div className="h-5 w-16 animate-pulse rounded bg-bg" />
        </li>
      ))}
    </ul>
  );
}

function Empty({ message, hint }: { message: string; hint?: string }) {
  return (
    <div className="px-5 py-12 text-center">
      <p className="text-sm font-medium text-text">{message}</p>
      {hint && <p className="mt-1 text-xs text-muted">{hint}</p>}
    </div>
  );
}
