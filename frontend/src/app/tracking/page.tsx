"use client";
import * as React from "react";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody } from "@/components/ui/Card";
import { ordersApi, type Order } from "@/lib/api";
import { OrderStateBadge } from "@/components/OrderStateBadge";

export default function TrackingPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const [orders, setOrders] = React.useState<Order[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    ordersApi.list()
      .then((r) => {
        const list = r.orders ?? [];
        setOrders(list.filter((o) => o.awb_number || ["booked", "in_transit"].includes(o.state)));
        setLoading(false);
      })
      .catch((e) => {
        setError((e as { message?: string }).message || "Failed to load");
        setLoading(false);
      });
  }, []);

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Tracking</h1>
        <p className="mt-1 text-sm text-muted">Live status across active shipments.</p>
      </header>

      <Card>
        <CardBody className="p-0">
          {loading ? (
            <div className="px-5 py-12 text-center text-sm text-muted">Loading…</div>
          ) : error ? (
            <div className="px-5 py-12 text-center">
              <p className="text-sm font-medium text-danger">{error}</p>
              <p className="mt-1 text-xs text-muted">Refresh to retry.</p>
            </div>
          ) : orders.length === 0 ? (
            <div className="px-5 py-16 text-center">
              <p className="text-sm font-medium">No active shipments yet.</p>
              <p className="mt-1 text-xs text-muted">
                Orders booked with a courier will show up here.
              </p>
            </div>
          ) : (
            <ul className="divide-y divide-border">
              {orders.map((o) => (
                <li key={o.id}>
                  <Link
                    href={`/orders/${o.id}`}
                    className="flex items-center justify-between gap-4 px-5 py-3 hover:bg-bg"
                  >
                    <div>
                      <div className="text-sm font-medium">{o.channel_order_id}</div>
                      <div className="mt-0.5 text-xs text-muted">
                        {o.awb_number ? `AWB ${o.awb_number} · ${o.carrier_code || ""}` : "awaiting AWB"}
                        {" · "}
                        {o.shipping_address.city}, {o.shipping_address.state}
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
    </div>
  );
}
