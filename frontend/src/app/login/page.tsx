"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Card, CardBody, CardDescription, CardHeader, CardTitle } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { useSession } from "@/lib/session";
import { Truck } from "lucide-react";

export default function LoginPage() {
  const router = useRouter();
  const { login, user, hasSeller, loading: sessionLoading } = useSession();
  const [email, setEmail] = React.useState("");
  const [name, setName] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // Already logged in? Skip the page.
  React.useEffect(() => {
    if (sessionLoading) return;
    if (user) router.replace(hasSeller ? "/dashboard" : "/onboarding");
  }, [user, hasSeller, sessionLoading, router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const r = await login(email.trim(), name.trim() || email.trim());
      router.replace(r.sellers.length > 0 ? "/dashboard" : "/onboarding");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="grid min-h-screen grid-cols-1 lg:grid-cols-2">
      {/* Left: hero */}
      <div className="hidden bg-gradient-to-br from-accent to-indigo-600 p-12 text-white lg:flex lg:flex-col lg:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-white/15 backdrop-blur">
            <Truck className="h-6 w-6" />
          </div>
          <span className="text-xl font-semibold">Pikshipp</span>
        </div>
        <div className="space-y-6 max-w-md">
          <h1 className="text-4xl font-semibold leading-tight">
            Ship smarter across every courier in India.
          </h1>
          <p className="text-white/85 text-lg">
            One dashboard for orders, allocations, tracking, COD, RTO, and reconciliation —
            built for D2C brands at every stage.
          </p>
          <ul className="space-y-2 text-sm text-white/80">
            <li>✓ 8+ courier integrations, real-time tracking webhooks</li>
            <li>✓ Smart allocation across cost / speed / reliability</li>
            <li>✓ COD remittance + weight reconciliation built in</li>
            <li>✓ Enterprise contracts with custom rates & SLAs</li>
          </ul>
        </div>
        <p className="text-xs text-white/60">© Pikshipp Logistics</p>
      </div>

      {/* Right: form */}
      <div className="flex items-center justify-center px-6 py-12">
        <div className="w-full max-w-sm">
          <div className="mb-8 lg:hidden">
            <div className="flex items-center gap-2">
              <Truck className="h-5 w-5 text-accent" />
              <span className="text-lg font-semibold">Pikshipp</span>
            </div>
          </div>
          <Card>
            <CardHeader>
              <CardTitle>Sign in</CardTitle>
              <CardDescription>
                Use your business email. New users will be guided through onboarding.
              </CardDescription>
            </CardHeader>
            <CardBody>
              <form onSubmit={handleSubmit} className="space-y-4">
                <Field label="Email" hint="We'll create your account if it doesn't exist">
                  <Input
                    type="email"
                    placeholder="founder@yourbrand.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    autoFocus
                  />
                </Field>
                <Field label="Your name">
                  <Input
                    placeholder="Riya Sharma"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </Field>
                {error && (
                  <div className="rounded-md border border-danger/20 bg-danger/5 px-3 py-2 text-sm text-danger">
                    {error}
                  </div>
                )}
                <Button type="submit" loading={submitting} className="w-full">
                  Continue
                </Button>
                <p className="text-center text-xs text-muted">
                  Dev mode active — Google OAuth wiring will replace this in prod.
                </p>
              </form>
            </CardBody>
          </Card>
        </div>
      </div>
    </div>
  );
}
