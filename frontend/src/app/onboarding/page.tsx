"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Card, CardBody, CardDescription, CardHeader, CardTitle } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { useSession } from "@/lib/session";
import { sellers } from "@/lib/api";
import { Check, ChevronRight, Truck } from "lucide-react";

type Step = 0 | 1 | 2;

export default function OnboardingPage() {
  const router = useRouter();
  const { user, hasSeller, loading, setActiveToken, refresh } = useSession();
  const [step, setStep] = React.useState<Step>(0);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const [legalName, setLegalName] = React.useState("");
  const [displayName, setDisplayName] = React.useState("");
  const [phone, setPhone] = React.useState("+91");
  const [billingEmail, setBillingEmail] = React.useState("");

  const [gstin, setGstin] = React.useState("");
  const [pan, setPan] = React.useState("");

  React.useEffect(() => {
    if (loading) return;
    if (!user) {
      router.replace("/login");
      return;
    }
    if (hasSeller) {
      router.replace("/dashboard");
    }
  }, [user, hasSeller, loading, router]);

  React.useEffect(() => {
    if (user && !billingEmail) setBillingEmail(user.email);
  }, [user, billingEmail]);

  async function provisionSeller() {
    setSubmitting(true);
    setError(null);
    try {
      const res = await sellers.provision({
        legal_name: legalName.trim(),
        display_name: displayName.trim() || legalName.trim(),
        primary_phone: phone.trim(),
        billing_email: billingEmail.trim(),
      });
      setActiveToken(res.token);
      setStep(1);
    } catch (e) {
      setError(extractError(e));
    } finally {
      setSubmitting(false);
    }
  }

  async function submitKyc() {
    setSubmitting(true);
    setError(null);
    try {
      await sellers.submitKYC({
        legal_name: legalName.trim(),
        gstin: gstin.trim().toUpperCase(),
        pan: pan.trim().toUpperCase(),
      });
      setStep(2);
      await refresh();
    } catch (e) {
      setError(extractError(e));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="min-h-screen bg-bg">
      <header className="border-b border-border bg-surface px-6 py-4">
        <div className="mx-auto flex max-w-6xl items-center gap-2">
          <Truck className="h-5 w-5 text-accent" />
          <span className="font-semibold">Pikshipp</span>
        </div>
      </header>

      <main className="mx-auto max-w-2xl px-4 py-10">
        <Stepper current={step} />

        {step === 0 && (
          <Card>
            <CardHeader>
              <CardTitle>Tell us about your business</CardTitle>
              <CardDescription>
                We'll provision your seller workspace and walk you through KYC next.
              </CardDescription>
            </CardHeader>
            <CardBody>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Legal entity name">
                  <Input
                    placeholder="Acme Logistics Pvt Ltd"
                    value={legalName}
                    onChange={(e) => setLegalName(e.target.value)}
                  />
                </Field>
                <Field label="Display name" hint="What buyers see in tracking">
                  <Input
                    placeholder="Acme"
                    value={displayName}
                    onChange={(e) => setDisplayName(e.target.value)}
                  />
                </Field>
                <Field label="Primary phone">
                  <Input
                    placeholder="+919999999999"
                    value={phone}
                    onChange={(e) => setPhone(e.target.value)}
                  />
                </Field>
                <Field label="Billing email">
                  <Input
                    type="email"
                    value={billingEmail}
                    onChange={(e) => setBillingEmail(e.target.value)}
                  />
                </Field>
              </div>
              {error && (
                <div className="mt-4 rounded-md border border-danger/20 bg-danger/5 px-3 py-2 text-sm text-danger">
                  {error}
                </div>
              )}
              <div className="mt-6 flex justify-end">
                <Button
                  onClick={provisionSeller}
                  loading={submitting}
                  disabled={!legalName || !phone}
                >
                  Continue <ChevronRight className="h-4 w-4" />
                </Button>
              </div>
            </CardBody>
          </Card>
        )}

        {step === 1 && (
          <Card>
            <CardHeader>
              <CardTitle>KYC verification</CardTitle>
              <CardDescription>
                We need your GSTIN and PAN to enable bookings.
                You'll be in sandbox mode until ops approves your application.
              </CardDescription>
            </CardHeader>
            <CardBody>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field
                  label="GSTIN"
                  hint="15-character format, e.g. 29AABCU9603R1ZX"
                  error={gstin && !isGSTIN(gstin) ? "Invalid GSTIN format" : undefined}
                >
                  <Input
                    placeholder="29AABCU9603R1ZX"
                    value={gstin}
                    onChange={(e) => setGstin(e.target.value.toUpperCase())}
                    maxLength={15}
                  />
                </Field>
                <Field
                  label="PAN"
                  hint="10-character format, e.g. AABCU9603R"
                  error={pan && !isPAN(pan) ? "Invalid PAN format" : undefined}
                >
                  <Input
                    placeholder="AABCU9603R"
                    value={pan}
                    onChange={(e) => setPan(e.target.value.toUpperCase())}
                    maxLength={10}
                  />
                </Field>
              </div>
              {error && (
                <div className="mt-4 rounded-md border border-danger/20 bg-danger/5 px-3 py-2 text-sm text-danger">
                  {error}
                </div>
              )}
              <div className="mt-6 flex justify-end gap-2">
                <Button variant="ghost" onClick={() => setStep(2)}>Skip for now</Button>
                <Button
                  onClick={submitKyc}
                  loading={submitting}
                  disabled={!isGSTIN(gstin) || !isPAN(pan)}
                >
                  Submit KYC <ChevronRight className="h-4 w-4" />
                </Button>
              </div>
            </CardBody>
          </Card>
        )}

        {step === 2 && (
          <Card>
            <CardBody className="py-12 text-center">
              <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-success/10">
                <Check className="h-6 w-6 text-success" />
              </div>
              <h2 className="text-xl font-semibold">You're all set</h2>
              <p className="mt-1 text-sm text-muted">
                Your seller workspace is ready. KYC review takes 1–2 business days.
              </p>
              <Button className="mt-6" onClick={() => router.replace("/dashboard")}>
                Go to dashboard
              </Button>
            </CardBody>
          </Card>
        )}
      </main>
    </div>
  );
}

function Stepper({ current }: { current: Step }) {
  const steps = ["Business details", "KYC", "Done"];
  return (
    <ol className="mb-8 grid grid-cols-3 gap-2">
      {steps.map((s, i) => (
        <li key={s} className="flex items-center gap-2">
          <div
            className={
              "flex h-7 w-7 items-center justify-center rounded-full text-xs font-medium " +
              (i < current
                ? "bg-success text-white"
                : i === current
                ? "bg-accent text-accent-fg"
                : "bg-bg text-muted border border-border")
            }
          >
            {i < current ? <Check className="h-4 w-4" /> : i + 1}
          </div>
          <span className={i === current ? "text-sm font-medium" : "text-sm text-muted"}>{s}</span>
        </li>
      ))}
    </ol>
  );
}

function extractError(e: unknown): string {
  if (typeof e === "object" && e && "message" in e) return String((e as { message: unknown }).message);
  return "Something went wrong";
}

// Indian GSTIN: 2-digit state code + 5 letters + 4 digits + 1 letter +
// 1 digit/letter + Z + 1 digit/letter. 15 chars total.
function isGSTIN(s: string): boolean {
  return /^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$/.test(s.trim().toUpperCase());
}
// PAN: 5 letters + 4 digits + 1 letter. 10 chars.
function isPAN(s: string): boolean {
  return /^[A-Z]{5}[0-9]{4}[A-Z]{1}$/.test(s.trim().toUpperCase());
}
