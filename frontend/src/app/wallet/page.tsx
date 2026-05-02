"use client";
import * as React from "react";
import { Shell } from "@/components/Shell";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/components/ui/Card";
import { walletApi, paiseToRupees, type WalletBalance } from "@/lib/api";

export default function WalletPage() {
  return <Shell><Inner /></Shell>;
}

function Inner() {
  const [bal, setBal] = React.useState<WalletBalance | null>(null);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    walletApi.balance().then((b) => { setBal(b); setLoading(false); }).catch(() => setLoading(false));
  }, []);

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Wallet</h1>
        <p className="mt-1 text-sm text-muted">Balance, holds, and credit limit.</p>
      </header>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Available</CardTitle>
            <CardDescription>balance + credit − holds</CardDescription>
          </CardHeader>
          <CardBody>
            <div className="text-3xl font-semibold">{loading || !bal ? "—" : paiseToRupees(bal.available)}</div>
          </CardBody>
        </Card>
        <Card>
          <CardHeader><CardTitle>Balance</CardTitle></CardHeader>
          <CardBody>
            <div className="text-2xl font-semibold">{loading || !bal ? "—" : paiseToRupees(bal.balance)}</div>
          </CardBody>
        </Card>
        <Card>
          <CardHeader><CardTitle>On hold</CardTitle></CardHeader>
          <CardBody>
            <div className="text-2xl font-semibold">{loading || !bal ? "—" : paiseToRupees(bal.hold_total)}</div>
          </CardBody>
        </Card>
      </div>

      {bal && (
        <Card>
          <CardHeader>
            <CardTitle>Credit & grace</CardTitle>
          </CardHeader>
          <CardBody className="grid gap-4 sm:grid-cols-2">
            <Detail label="Credit limit" value={paiseToRupees(bal.credit_limit)} />
            <Detail label="Grace amount" value={paiseToRupees(bal.grace_amount)} />
            <Detail label="Status" value={bal.status} />
          </CardBody>
        </Card>
      )}
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wider text-muted">{label}</div>
      <div className="mt-1 text-base font-medium">{value}</div>
    </div>
  );
}
