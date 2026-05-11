"use client";
import * as React from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { ordersApi, paiseToRupees, type Order } from "@/lib/api";
import { QRCodeSVG } from "qrcode.react";
import { ArrowLeft, Check, Copy, Smartphone } from "lucide-react";

// Read from env at module load. We allow the merchant to swap their VPA + name
// without a code change — set NEXT_PUBLIC_UPI_VPA and NEXT_PUBLIC_UPI_PAYEE.
const UPI_VPA = process.env.NEXT_PUBLIC_UPI_VPA || "pikshipp@upi";
const UPI_PAYEE = process.env.NEXT_PUBLIC_UPI_PAYEE || "Pikshipp";
const STATIC_QR_IMG = process.env.NEXT_PUBLIC_UPI_QR_IMAGE || "/payment-qr.png";

export default function PayPage() {
  return (
    <Shell>
      <Inner />
    </Shell>
  );
}

function Inner() {
  const params = useParams();
  const router = useRouter();
  const id = String(params.id);
  const [order, setOrder] = React.useState<Order | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [copied, setCopied] = React.useState(false);
  const [imgOk, setImgOk] = React.useState(true);

  React.useEffect(() => {
    ordersApi.get(id).then(setOrder).catch((e) =>
      setError((e as { message?: string }).message || "Not found"),
    );
  }, [id]);

  if (error) {
    return (
      <Card>
        <CardBody className="py-12 text-center">
          <p className="text-sm font-medium text-danger">{error}</p>
          <Button variant="ghost" className="mt-4" onClick={() => router.back()}>
            Go back
          </Button>
        </CardBody>
      </Card>
    );
  }
  if (!order) return <p className="text-sm text-muted">Loading…</p>;

  const amountRupees = (order.total_paise / 100).toFixed(2);
  // UPI deep-link spec: https://www.npci.org.in/PDF/npci/upi/UPI-Linking-Specs.pdf
  // pa=payee VPA, pn=payee name, am=amount in rupees, tn=transaction note,
  // tr=transaction reference, cu=currency. tr is what we'd reconcile against
  // once we wire a webhook.
  const upiURL =
    `upi://pay?pa=${encodeURIComponent(UPI_VPA)}` +
    `&pn=${encodeURIComponent(UPI_PAYEE)}` +
    `&am=${amountRupees}` +
    `&cu=INR` +
    `&tn=${encodeURIComponent("Pikshipp " + order.channel_order_id)}` +
    `&tr=${encodeURIComponent(order.id)}`;

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(upiURL);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore — clipboard can fail in non-https or older browsers */
    }
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <Link
          href={`/orders/${id}`}
          className="inline-flex items-center gap-1 text-sm text-muted hover:text-text"
        >
          <ArrowLeft className="h-4 w-4" /> Back to order
        </Link>
      </div>

      <header>
        <h1 className="text-2xl font-semibold">Pay for this order</h1>
        <p className="mt-1 text-sm text-muted">
          Scan the QR with any UPI app — the amount is locked to{" "}
          <strong>{paiseToRupees(order.total_paise)}</strong>. No app integration yet,
          so a teammate will mark this paid once the transfer lands.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Order {order.channel_order_id}</CardTitle>
          <CardDescription>
            {order.buyer_name} · {order.payment_method.toUpperCase()} ·{" "}
            {paiseToRupees(order.total_paise)} total
          </CardDescription>
        </CardHeader>
        <CardBody className="space-y-6">
          <div className="grid gap-6 sm:grid-cols-2">
            <div className="flex flex-col items-center justify-center rounded-lg border border-border bg-bg/30 p-4">
              <div className="rounded-md bg-white p-4">
                <QRCodeSVG value={upiURL} size={200} level="M" />
              </div>
              <div className="mt-3 text-center text-xs text-muted">
                Dynamic UPI QR · amount + ref locked
              </div>
            </div>

            <div className="flex flex-col justify-between gap-4">
              <div>
                <div className="text-xs uppercase tracking-wider text-muted">
                  Amount to pay
                </div>
                <div className="mt-1 text-3xl font-semibold tabular-nums">
                  {paiseToRupees(order.total_paise)}
                </div>
                <div className="mt-4 space-y-1 text-sm">
                  <Row label="Payee">{UPI_PAYEE}</Row>
                  <Row label="UPI ID">
                    <span className="font-mono text-xs">{UPI_VPA}</span>
                  </Row>
                  <Row label="Reference">
                    <span className="font-mono text-xs">{order.channel_order_id}</span>
                  </Row>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                <a
                  href={upiURL}
                  className="inline-flex h-10 items-center justify-center gap-2 rounded-md bg-accent px-4 text-sm font-medium text-accent-fg hover:bg-accent/90"
                >
                  <Smartphone className="h-4 w-4" /> Open in UPI app
                </a>
                <Button variant="secondary" onClick={copyLink}>
                  {copied ? (
                    <>
                      <Check className="h-4 w-4" /> Copied
                    </>
                  ) : (
                    <>
                      <Copy className="h-4 w-4" /> Copy UPI link
                    </>
                  )}
                </Button>
              </div>
            </div>
          </div>
        </CardBody>
      </Card>

      {imgOk && (
        <Card>
          <CardHeader>
            <CardTitle>Alternative: scan our saved QR</CardTitle>
            <CardDescription>
              If your app can't read the dynamic QR, scan this and enter the amount
              manually: <strong>{paiseToRupees(order.total_paise)}</strong>.
            </CardDescription>
          </CardHeader>
          <CardBody className="flex justify-center">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={STATIC_QR_IMG}
              alt="Static UPI QR"
              className="h-56 w-56 rounded-md border border-border bg-white object-contain p-2"
              onError={() => setImgOk(false)}
            />
          </CardBody>
        </Card>
      )}

      <Card>
        <CardBody className="text-xs text-muted">
          We don't auto-reconcile payments yet — once Razorpay / Stripe is wired
          in, this screen will confirm payment in real time. For now, a teammate
          will mark this order as paid after the transfer reflects.
        </CardBody>
      </Card>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-muted">{label}</span>
      <span className="truncate text-right">{children}</span>
    </div>
  );
}
