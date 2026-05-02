"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { useSession } from "@/lib/session";
import { Truck } from "lucide-react";

// The backend's Google OAuth callback redirects here with credentials in the
// URL fragment (#token=...&expires_at=...&new_user=true|false). Fragments
// never reach the server, which keeps the opaque session token out of any
// reverse-proxy access logs.
//
// On error, the backend redirects with #error=<code>. We bounce to /login
// with that code as a query param so LoginPage can render a readable message.
export default function AuthCallbackPage() {
  const router = useRouter();
  const { setActiveToken } = useSession();
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (typeof window === "undefined") return;

    const fragment = window.location.hash.startsWith("#")
      ? window.location.hash.slice(1)
      : window.location.hash;
    const params = new URLSearchParams(fragment);

    const errCode = params.get("error");
    if (errCode) {
      router.replace(`/login?error=${encodeURIComponent(errCode)}`);
      return;
    }

    const token = params.get("token");
    if (!token) {
      setErrorMsg("Sign-in completed but no token was returned. Please try again.");
      return;
    }
    const newUser = params.get("new_user") === "true";

    // Strip the fragment from the URL so the token isn't visible to anyone
    // looking over the user's shoulder while we navigate away.
    history.replaceState(null, "", window.location.pathname);

    setActiveToken(token);
    router.replace(newUser ? "/onboarding" : "/dashboard");
  }, [router, setActiveToken]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg">
      <div className="flex flex-col items-center gap-4 text-center">
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-accent/10 text-accent">
          <Truck className="h-6 w-6" />
        </div>
        {errorMsg ? (
          <>
            <p className="max-w-sm text-sm text-danger">{errorMsg}</p>
            <button
              onClick={() => router.replace("/login")}
              className="text-sm text-accent underline"
            >
              Back to sign in
            </button>
          </>
        ) : (
          <>
            <p className="text-sm text-muted">Finishing sign-in…</p>
            <div className="h-1 w-32 overflow-hidden rounded-full bg-bg-subtle">
              <div className="h-full w-1/2 animate-pulse rounded-full bg-accent" />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
