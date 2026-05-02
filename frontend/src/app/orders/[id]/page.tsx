"use client";
import * as React from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { ordersApi, paiseToRupees, type Order } from "@/lib/api";
import { OrderStateBadge } from "@/components/OrderStateBadge";
import { ArrowLeft } from "lucide-react";

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

  React.useEffect(() => {
    ordersApi.get(id).then(setOrder).catch((e) => setError((e as { message?: string }).message || "Not found"));
  }, [id]);

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
    </div>
  );
}
