"use client";
import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  LayoutDashboard,
  Package,
  Truck,
  Wallet as WalletIcon,
  Building2,
  Calculator,
  BarChart3,
  LogOut,
  Menu as MenuIcon,
  X,
} from "lucide-react";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/cn";

const NAV = [
  { label: "Dashboard", href: "/dashboard", icon: LayoutDashboard },
  { label: "Orders", href: "/orders", icon: Package },
  { label: "Tracking", href: "/tracking", icon: Truck },
  { label: "Pricing", href: "/pricing", icon: Calculator },
  { label: "Reports", href: "/reports", icon: BarChart3 },
  { label: "Wallet", href: "/wallet", icon: WalletIcon },
  { label: "Enterprise", href: "/enterprise", icon: Building2 },
];

export function Shell({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const path = usePathname();
  const { user, hasSeller, loading, logout } = useSession();
  const [mobileOpen, setMobileOpen] = React.useState(false);

  React.useEffect(() => {
    if (loading) return;
    if (!user) router.replace("/login");
    else if (!hasSeller) router.replace("/onboarding");
  }, [user, hasSeller, loading, router]);

  // Close the mobile drawer when the route changes (clicking a nav link).
  React.useEffect(() => {
    setMobileOpen(false);
  }, [path]);

  if (loading || !user || !hasSeller) {
    return (
      <div className="flex min-h-screen items-center justify-center text-muted">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-current border-t-transparent" />
      </div>
    );
  }

  const initials = (user.name || user.email).charAt(0).toUpperCase();

  return (
    <div className="flex min-h-screen">
      {/* Desktop sidebar */}
      <aside className="hidden w-60 flex-col border-r border-border bg-surface md:flex">
        <div className="flex h-16 items-center gap-2 border-b border-border px-5">
          <Truck className="h-5 w-5 text-accent" />
          <span className="font-semibold">Pikshipp</span>
        </div>
        <Nav path={path} />
        <div className="border-t border-border p-3">
          <button
            onClick={logout}
            className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-text hover:bg-bg"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </button>
        </div>
      </aside>

      {/* Mobile drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div
            className="absolute inset-0 bg-black/40"
            onClick={() => setMobileOpen(false)}
            aria-hidden="true"
          />
          <aside className="absolute left-0 top-0 flex h-full w-72 flex-col border-r border-border bg-surface shadow-xl">
            <div className="flex h-16 items-center justify-between border-b border-border px-5">
              <div className="flex items-center gap-2">
                <Truck className="h-5 w-5 text-accent" />
                <span className="font-semibold">Pikshipp</span>
              </div>
              <button
                onClick={() => setMobileOpen(false)}
                aria-label="Close menu"
                className="rounded-md p-1 text-muted hover:bg-bg"
              >
                <X className="h-5 w-5" />
              </button>
            </div>
            <Nav path={path} />
            <div className="border-t border-border p-3">
              <button
                onClick={logout}
                className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-text hover:bg-bg"
              >
                <LogOut className="h-4 w-4" />
                Sign out
              </button>
            </div>
          </aside>
        </div>
      )}

      {/* Main */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 items-center justify-between border-b border-border bg-surface px-4 md:px-6">
          <div className="flex items-center gap-2">
            <button
              className="rounded-md p-2 text-muted hover:bg-bg md:hidden"
              onClick={() => setMobileOpen(true)}
              aria-label="Open menu"
            >
              <MenuIcon className="h-5 w-5" />
            </button>
            <div className="flex items-center gap-2 md:hidden">
              <Truck className="h-5 w-5 text-accent" />
              <span className="font-semibold">Pikshipp</span>
            </div>
          </div>

          <Link
            href="/account"
            aria-label="Account"
            className={cn(
              "flex items-center gap-3 rounded-md px-2 py-1 hover:bg-bg",
              path.startsWith("/account") && "bg-bg",
            )}
          >
            <div className="hidden text-right sm:block">
              <div className="text-sm font-medium leading-tight">
                {user.name || user.email}
              </div>
              <div className="text-xs text-muted">{user.email}</div>
            </div>
            <div className="flex h-9 w-9 items-center justify-center rounded-full bg-accent/10 text-sm font-medium text-accent">
              {initials}
            </div>
          </Link>
        </header>
        <main className="flex-1 px-4 py-6 md:px-6 md:py-8">{children}</main>
      </div>
    </div>
  );
}

function Nav({ path }: { path: string }) {
  return (
    <nav className="flex-1 space-y-1 p-3">
      {NAV.map((item) => {
        const active = path === item.href || path.startsWith(item.href + "/");
        return (
          <Link
            key={item.href}
            href={item.href}
            className={cn(
              "flex items-center gap-3 rounded-md px-3 py-2 text-sm",
              active
                ? "bg-accent/10 text-accent font-medium"
                : "text-text hover:bg-bg",
            )}
          >
            <item.icon className="h-4 w-4" />
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}
