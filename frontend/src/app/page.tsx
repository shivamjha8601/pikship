"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { useSession } from "@/lib/session";

// Landing → router based on session state.
// Unauthenticated → /login
// Authenticated, no seller → /onboarding
// Authenticated, has seller → /dashboard
export default function Home() {
  const router = useRouter();
  const { user, hasSeller, loading } = useSession();

  React.useEffect(() => {
    if (loading) return;
    if (!user) router.replace("/login");
    else if (!hasSeller) router.replace("/onboarding");
    else router.replace("/dashboard");
  }, [user, hasSeller, loading, router]);

  return (
    <div className="flex min-h-screen items-center justify-center text-muted">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent" />
    </div>
  );
}
