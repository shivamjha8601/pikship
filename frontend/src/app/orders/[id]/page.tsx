"use client";
import * as React from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { ordersApi, paiseToRupees, shipmentsApi, type Order, type Shipment, type TrackingEvent } from "@/lib/api";
import { OrderStateBadge } from "@/components/OrderStateBadge";
import { ArrowLeft, IndianRupee, RefreshCw, Send, Truck } from "lucide-react";
import { LinkButton } from "@/components/ui/Button";

export default function OrderDetailPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const params = useParams();
  const router = useRouter();
  const id = String(params.id);
  const [order, setOrder] = React.useState<Order | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [cancelling, setCancelling] = React.useState(false);
  const [booking, setBooking] = React.useState(false);
  const [bookErr, setBookErr] = React.useState<string | null>(null);
  const [shipment, setShipment] = React.useState<Shipment | null>(null);
  const [events, setEvents] = React.useState<TrackingEvent[]>([]);
  const [refreshing, setRefreshing] = React.useState(false);
  const [trackErr, setTrackErr] = React.useState<string | null>(null);

  const loadOrder = React.useCallback(() => {
    ordersApi.get(id).then(setOrder).catch((e) =>
      setError((e as { message?: string }).message || "Not found"),
    );
  }, [id]);

  React.useEffect(() => {
    loadOrder();
  }, [loadOrder]);

  // If the order is already booked when the page loads (e.g. user revisits
  // after creating + booking earlier), look up the shipment so the Refresh
  // button has something to call and any prior events render automatically.
  React.useEffect(() => {
    if (!order?.awb_number || shipment) return;
    let cancelled = false;
    (async () => {
      try {
        const sh = await ordersApi.shipment(order.id);
        if (cancelled) return;
        setShipment(sh);
        const evs = (await shipmentsApi.listEvents(sh.id)) || [];
        if (cancelled) return;
        setEvents(evs);
      } catch {
        // Silent — if no shipment exists yet the page falls back to the
        // standalone AWB header and the user can still click Refresh once
        // they re-book.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [order?.awb_number, order?.id, shipment]);

  async function book() {
    setBooking(true);
    setBookErr(null);
    try {
      const ship = await ordersApi.book(id);
      setShipment(ship);
      loadOrder();
      // Auto-pull initial tracking events.
      const evs = (await shipmentsApi.refresh(ship.id)) || [];
      setEvents(evs);
    } catch (e) {
      setBookErr((e as { message?: string }).message || "Failed to book");
    } finally {
      setBooking(false);
    }
  }

  async function refreshTracking() {
    if (!shipment) return;
    setRefreshing(true);
    setTrackErr(null);
    try {
      const evs = (await shipmentsApi.refresh(shipment.id)) || [];
      setEvents(evs);
      // The poll may have advanced the order state via shipment transitions;
      // re-load so the header badge + CTAs reflect the new state.
      loadOrder();
    } catch (e) {
      setTrackErr((e as { message?: string }).message || "Failed to refresh");
    } finally {
      setRefreshing(false);
    }
  }

  async function cancel() {
    if (!order) return;
    if (!confirm(`Cancel order ${order.channel_order_id}?`)) return;
    setCancelling(true);
    try {
      await ordersApi.cancel(order.id, "Cancelled from dashboard");
      const fresh = await ordersApi.get(order.id);
      setOrder(fresh);
    } catch (e) {
      alert("Cancel failed: " + ((e as { message?: string }).message || "unknown"));
    } finally {
      setCancelling(false);
    }
  }

  if (error) {
    return (
      <Card>
        <CardBody className="py-12 text-center">
          <p className="text-sm font-medium text-danger">{error}</p>
          <Button variant="ghost" className="mt-4" onClick={() => router.back()}>Go back</Button>
        </CardBody>
      </Card>
    );
  }
  if (!order) return <p className="text-sm text-muted">Loading…</p>;

  const cancellable = ["draft", "ready", "allocating", "booked"].includes(order.state);

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <Link href="/orders" className="text-sm text-muted hover:text-text inline-flex items-center gap-1">
          <ArrowLeft className="h-4 w-4" /> Back to orders
        </Link>
      </div>

      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-semibold">{order.channel_order_id}</h1>
            <OrderStateBadge state={order.state} />
          </div>
          <p className="mt-1 text-sm text-muted">
            {order.channel} · created {new Date(order.created_at).toLocaleString()}
          </p>
        </div>
        <div className="flex gap-2">
          {order.payment_method === "prepaid" &&
            ["draft", "ready", "allocating"].includes(order.state) && (
              <LinkButton href={`/orders/${order.id}/pay`}>
                <IndianRupee className="h-4 w-4" /> Pay {paiseToRupees(order.total_paise)}
              </LinkButton>
            )}
          {!order.awb_number && ["draft", "ready"].includes(order.state) && (
            <Button onClick={book} loading={booking}>
              <Send className="h-4 w-4" /> Book shipment
            </Button>
          )}
          {cancellable && (
            <Button variant="danger" loading={cancelling} onClick={cancel}>Cancel order</Button>
          )}
        </div>
      </header>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle>Items</CardTitle></CardHeader>
          <CardBody className="p-0">
            <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-bg/50 text-xs uppercase tracking-wider text-muted">
                <tr>
                  <th className="px-5 py-2 text-left font-medium">SKU</th>
                  <th className="px-5 py-2 text-left font-medium">Name</th>
                  <th className="px-5 py-2 text-right font-medium">Qty</th>
                  <th className="px-5 py-2 text-right font-medium">Price</th>
                  <th className="px-5 py-2 text-right font-medium">Subtotal</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {order.lines.map((l) => (
                  <tr key={l.line_no}>
                    <td className="px-5 py-3 font-mono text-xs">{l.sku}</td>
                    <td className="px-5 py-3">{l.name}</td>
                    <td className="px-5 py-3 text-right">{l.quantity}</td>
                    <td className="px-5 py-3 text-right">{paiseToRupees(l.unit_price_paise)}</td>
                    <td className="px-5 py-3 text-right">{paiseToRupees(l.unit_price_paise * l.quantity)}</td>
                  </tr>
                ))}
              </tbody>
              <tfoot className="border-t border-border">
                <tr><td colSpan={4} className="px-5 py-2 text-right text-muted">Subtotal</td><td className="px-5 py-2 text-right">{paiseToRupees(order.subtotal_paise)}</td></tr>
                <tr><td colSpan={4} className="px-5 py-2 text-right text-muted">Shipping</td><td className="px-5 py-2 text-right">{paiseToRupees(order.shipping_paise)}</td></tr>
                <tr><td colSpan={4} className="px-5 py-2 text-right font-semibold">Total</td><td className="px-5 py-2 text-right font-semibold">{paiseToRupees(order.total_paise)}</td></tr>
              </tfoot>
            </table>
            </div>
          </CardBody>
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader><CardTitle>Buyer</CardTitle></CardHeader>
            <CardBody className="space-y-1 text-sm">
              <div className="font-medium">{order.buyer_name}</div>
              <div className="text-muted">{order.buyer_phone}</div>
              {order.buyer_email && <div className="text-muted">{order.buyer_email}</div>}
            </CardBody>
          </Card>
          <Card>
            <CardHeader><CardTitle>Ship to</CardTitle></CardHeader>
            <CardBody className="space-y-1 text-sm">
              <div>{order.shipping_address.line1}</div>
              {order.shipping_address.line2 && <div>{order.shipping_address.line2}</div>}
              <div>{order.shipping_address.city}, {order.shipping_address.state}</div>
              <div className="font-mono text-muted">{order.shipping_pincode}</div>
            </CardBody>
          </Card>
          <Card>
            <CardHeader><CardTitle>Payment & package</CardTitle></CardHeader>
            <CardBody className="space-y-2 text-sm">
              <div className="flex justify-between"><span className="text-muted">Method</span><span className="uppercase">{order.payment_method}</span></div>
              {order.cod_amount_paise > 0 && (
                <div className="flex justify-between"><span className="text-muted">COD amount</span><span>{paiseToRupees(order.cod_amount_paise)}</span></div>
              )}
              <div className="flex justify-between"><span className="text-muted">Weight</span><span>{order.package_weight_g} g</span></div>
              <div className="flex justify-between"><span className="text-muted">Dimensions</span><span>{order.package_length_mm}×{order.package_width_mm}×{order.package_height_mm} mm</span></div>
              {order.awb_number && (
                <div className="flex justify-between"><span className="text-muted">AWB</span><span className="font-mono">{order.awb_number}</span></div>
              )}
            </CardBody>
          </Card>
        </div>
      </div>

      {bookErr && (
        <div className="rounded-md border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
          {bookErr}
        </div>
      )}

      {(shipment || order.awb_number) && (
        <Card>
          <CardHeader className="flex items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Truck className="h-4 w-4 text-muted" />
                Tracking
              </CardTitle>
              <p className="mt-1 text-xs text-muted">
                AWB <span className="font-mono">{shipment?.awb || order.awb_number}</span> · refresh to pull latest from the carrier
              </p>
            </div>
            <Button size="sm" variant="secondary" onClick={refreshTracking} loading={refreshing}>
              <RefreshCw className="h-4 w-4" /> Refresh
            </Button>
          </CardHeader>
          <CardBody>
            {trackErr && <p className="mb-3 text-sm text-danger">{trackErr}</p>}
            {events.length === 0 ? (
              <p className="text-sm text-muted">
                No tracking events yet. Click <strong>Refresh</strong> to pull
                from the carrier.
              </p>
            ) : (
              <ol className="relative space-y-3 border-l border-border pl-5">
                {events.map((e, i) => (
                  <li key={`${e.AWB}-${e.OccurredAt}-${i}`} className="relative">
                    <span
                      className={
                        "absolute -left-[26px] top-1 h-3 w-3 rounded-full border-2 border-surface " +
                        (i === 0 ? "bg-accent" : "bg-muted")
                      }
                    />
                    <div className="text-sm font-medium">{e.RawStatus}</div>
                    <div className="text-xs text-muted">
                      {new Date(e.OccurredAt).toLocaleString()}
                      {e.Location && ` · ${e.Location}`}
                      {e.CanonicalStatus && ` · ${e.CanonicalStatus}`}
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </CardBody>
        </Card>
      )}
    </div>
  );
}
