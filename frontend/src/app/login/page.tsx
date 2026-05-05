"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Card, CardBody, CardDescription, CardHeader, CardTitle } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { useSession } from "@/lib/session";
import { Truck } from "lucide-react";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8081";
const SHOW_DEV_LOGIN = process.env.NEXT_PUBLIC_DEV_MODE === "true";

const GOOGLE_OAUTH_ERRORS: Record<string, string> = {
  state_mismatch: "Sign-in expired. Please try again.",
  nonce_mismatch: "Sign-in could not be verified. Please try again.",
  exchange_failed: "Google declined the sign-in. Please try again.",
  id_token_invalid: "Google sign-in token failed verification. Please try again.",
  user_upsert_failed: "We couldn't create your account. Please try again.",
  session_issue_failed: "Sign-in succeeded but we couldn't issue a session.",
  missing_code: "Google did not return a sign-in code.",
  access_denied: "You cancelled the Google sign-in.",
};

export default function LoginPage() {
  const router = useRouter();
  const { login, user, hasSeller, loading: sessionLoading } = useSession();
  const [email, setEmail] = React.useState("");
  const [name, setName] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // If we got bounced back from /auth/callback with ?error=..., surface it.
  // Read window.location directly to avoid pulling /login out of static
  // prerendering (which useSearchParams would force).
  React.useEffect(() => {
    if (typeof window === "undefined") return;
    const e = new URLSearchParams(window.location.search).get("error");
    if (e) setError(GOOGLE_OAUTH_ERRORS[e] || `Sign-in failed (${e})`);
  }, []);

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

  function handleGoogleSignIn() {
    // Top-level navigation, NOT a fetch — the state/nonce cookies must be set
    // on the backend's own origin so they're visible when Google redirects
    // back to /v1/auth/google/callback there.
    window.location.href = `${API_BASE}/v1/auth/google/start`;
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
              <div className="space-y-4">
                {error && (
                  <div className="rounded-md border border-danger/20 bg-danger/5 px-3 py-2 text-sm text-danger">
                    {error}
                  </div>
                )}
                <button
                  type="button"
                  onClick={handleGoogleSignIn}
                  className="flex w-full items-center justify-center gap-3 rounded-md border border-border bg-white px-4 py-2.5 text-sm font-medium text-fg shadow-sm transition hover:bg-bg-subtle"
                >
                  <GoogleLogo />
                  Continue with Google
                </button>
                {SHOW_DEV_LOGIN && (
                  <>
                    <div className="flex items-center gap-3 text-xs uppercase tracking-wide text-muted">
                      <div className="h-px flex-1 bg-border" />
                      <span>or dev login</span>
                      <div className="h-px flex-1 bg-border" />
                    </div>
                    <form onSubmit={handleSubmit} className="space-y-4">
                      <Field label="Email" hint="We'll create your account if it doesn't exist">
                        <Input
                          type="email"
                          placeholder="founder@yourbrand.com"
                          value={email}
                          onChange={(e) => setEmail(e.target.value)}
                          required
                        />
                      </Field>
                      <Field label="Your name">
                        <Input
                          placeholder="Riya Sharma"
                          value={name}
                          onChange={(e) => setName(e.target.value)}
                        />
                      </Field>
                      <Button type="submit" loading={submitting} className="w-full" variant="secondary">
                        Continue with email
                      </Button>
                    </form>
                  </>
                )}
              </div>
            </CardBody>
          </Card>
        </div>
      </div>
    </div>
  );
}

function GoogleLogo() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
      <path
        fill="#4285F4"
        d="M17.64 9.2c0-.637-.057-1.251-.164-1.84H9v3.481h4.844a4.14 4.14 0 0 1-1.796 2.717v2.258h2.908c1.702-1.567 2.684-3.874 2.684-6.615z"
      />
      <path
        fill="#34A853"
        d="M9 18c2.43 0 4.467-.806 5.956-2.184l-2.908-2.258c-.806.54-1.837.86-3.048.86-2.344 0-4.328-1.584-5.036-3.711H.957v2.332A8.997 8.997 0 0 0 9 18z"
      />
      <path
        fill="#FBBC05"
        d="M3.964 10.707A5.41 5.41 0 0 1 3.682 9c0-.593.102-1.17.282-1.707V4.961H.957A8.997 8.997 0 0 0 0 9c0 1.452.348 2.827.957 4.039l3.007-2.332z"
      />
      <path
        fill="#EA4335"
        d="M9 3.58c1.321 0 2.508.454 3.44 1.345l2.582-2.58C13.463.891 11.426 0 9 0A8.997 8.997 0 0 0 .957 4.961L3.964 7.293C4.672 5.166 6.656 3.58 9 3.58z"
      />
    </svg>
  );
}
