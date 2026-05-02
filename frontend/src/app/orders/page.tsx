"use client";
import * as React from "react";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Badge";
import { Input } from "@/components/ui/Input";
import { ordersApi, paiseToRupees, type Order } from "@/lib/api";
import { OrderStateBadge } from "@/components/OrderStateBadge";
import { Plus, Search } from "lucide-react";

export default function OrdersPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const [orders, setOrders] = React.useState<Order[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [q, setQ] = React.useState("");
  const [state, setState] = React.useState<string>("");

  React.useEffect(() => {
    ordersApi.list().then(r => { setOrders(r.orders); setLoading(false); });
  }, []);

  const filtered = orders.filter(o => {
    if (state && o.state !== state) return false;
    if (q) {
      const needle = q.toLowerCase();
      if (
        !o.channel_order_id.toLowerCase().includes(needle) &&
        !o.buyer_name.toLowerCase().includes(needle) &&
        !(o.awb_number || "").toLowerCase().includes(needle)
      ) return false;
    }
    return true;
  });

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">Orders</h1>
          <p className="mt-1 text-sm text-muted">{orders.length.toLocaleString()} total</p>
        </div>
        <Link href="/orders/new">
          <Button><Plus className="h-4 w-4" /> Create order</Button>
        </Link>
      </header>

      <Card>
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3">
          <div className="relative flex-1 min-w-[220px]">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
            <Input
              placeholder="Search order ID, buyer, or AWB"
              className="pl-9"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <select
            value={state}
            onChange={(e) => setState(e.target.value)}
            className="h-10 rounded-md border border-border bg-surface px-3 text-sm"
          >
            <option value="">All states</option>
            {["draft", "ready", "allocating", "booked", "in_transit", "delivered", "cancelled", "rto", "closed"].map((s) => (
              <option key={s} value={s}>{s.replace("_", " ")}</option>
            ))}
          </select>
        </div>

        <CardBody className="p-0">
          {loading ? (
            <div className="px-5 py-12 text-center text-sm text-muted">Loading…</div>
          ) : filtered.length === 0 ? (
            <div className="px-5 py-16 text-center">
              <p className="text-sm font-medium">No orders match your filter.</p>
              <p className="mt-1 text-xs text-muted">
                {orders.length === 0 ? "Create your first order to get started." : "Try clearing filters."}
              </p>
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-bg/50 text-xs uppercase tracking-wider text-muted">
                <tr>
                  <th className="px-5 py-3 text-left font-medium">Order</th>
                  <th className="px-5 py-3 text-left font-medium">Buyer</th>
                  <th className="px-5 py-3 text-left font-medium">Destination</th>
                  <th className="px-5 py-3 text-left font-medium">Total</th>
                  <th className="px-5 py-3 text-left font-medium">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {filtered.map((o) => (
                  <tr key={o.id} className="hover:bg-bg">
                    <td className="px-5 py-3">
                      <Link href={`/orders/${o.id}`} className="font-medium text-accent hover:underline">
                        {o.channel_order_id}
                      </Link>
                      <div className="text-xs text-muted">{o.channel}</div>
                    </td>
                    <td className="px-5 py-3">
                      <div>{o.buyer_name}</div>
                      <div className="text-xs text-muted">{o.buyer_phone}</div>
                    </td>
                    <td className="px-5 py-3">
                      {o.shipping_address.city}, {o.shipping_address.state}
                      <div className="text-xs text-muted">{o.shipping_pincode}</div>
                    </td>
                    <td className="px-5 py-3">
                      {paiseToRupees(o.total_paise)}
                      <div className="text-xs text-muted uppercase">{o.payment_method}</div>
                    </td>
                    <td className="px-5 py-3"><OrderStateBadge state={o.state} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardBody>
      </Card>
    </div>
  );
}
