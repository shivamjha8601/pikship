"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { auth, getToken, setToken, type User, type SellerMembership } from "@/lib/api";

type SessionState = {
  user: User | null;
  sellers: SellerMembership[];
  hasSeller: boolean; // user has at least one membership
  loading: boolean;
};

type SessionContextValue = SessionState & {
  refresh: () => Promise<void>;
  login: (email: string, name: string) => Promise<{ user: User; sellers: SellerMembership[] }>;
  logout: () => Promise<void>;
  setActiveToken: (token: string) => void;
};

const SessionContext = React.createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [state, setState] = React.useState<SessionState>({
    user: null,
    sellers: [],
    hasSeller: false,
    loading: true,
  });

  const refresh = React.useCallback(async () => {
    const tok = getToken();
    if (!tok) {
      setState({ user: null, sellers: [], hasSeller: false, loading: false });
      return;
    }
    try {
      const res = await auth.me();
      const sellers = res.sellers || [];
      // If the user has memberships but the current token isn't bound to a
      // seller, bind it to the first one — otherwise every seller-scoped
      // endpoint 403s with seller_not_selected. Login + Google OAuth both
      // mint tokens with no seller; this is the catch.
      if (sellers.length > 0 && !res.active_seller_id) {
        try {
          const selected = await auth.selectSeller(sellers[0].seller_id);
          setToken(selected.token);
        } catch {
          // Tolerate — we still have a valid (un-scoped) session and the
          // user can land on /onboarding or /login without crashing.
        }
      }
      setState({
        user: res.user,
        sellers,
        hasSeller: sellers.length > 0,
        loading: false,
      });
    } catch {
      setToken(null);
      setState({ user: null, sellers: [], hasSeller: false, loading: false });
    }
  }, []);

  React.useEffect(() => { refresh(); }, [refresh]);

  // Listen for the global 401 signal from the API client. If a request comes
  // back unauthorized, the api client clears the token and dispatches this
  // event — we react by resetting state and routing to /login.
  React.useEffect(() => {
    const handler = () => {
      setState({ user: null, sellers: [], hasSeller: false, loading: false });
      router.push("/login");
    };
    window.addEventListener("pikshipp:unauthorized", handler);
    return () => window.removeEventListener("pikshipp:unauthorized", handler);
  }, [router]);

  const login = React.useCallback(async (email: string, name: string) => {
    const res = await auth.devLogin(email, name);
    setToken(res.token);
    setState({
      user: res.user,
      sellers: res.sellers || [],
      hasSeller: !!(res.sellers && res.sellers.length > 0),
      loading: false,
    });
    return { user: res.user, sellers: res.sellers || [] };
  }, []);

  const logout = React.useCallback(async () => {
    try { await auth.logout(); } catch { /* fine */ }
    setToken(null);
    setState({ user: null, sellers: [], hasSeller: false, loading: false });
    router.push("/login");
  }, [router]);

  const setActiveToken = React.useCallback((token: string) => {
    setToken(token);
    refresh();
  }, [refresh]);

  return (
    <SessionContext.Provider value={{ ...state, refresh, login, logout, setActiveToken }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession() {
  const ctx = React.useContext(SessionContext);
  if (!ctx) throw new Error("useSession must be used inside SessionProvider");
  return ctx;
}
