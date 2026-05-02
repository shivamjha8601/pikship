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
      setState({
        user: res.user,
        sellers: res.sellers || [],
        hasSeller: !!(res.sellers && res.sellers.length > 0),
        loading: false,
      });
    } catch {
      setToken(null);
      setState({ user: null, sellers: [], hasSeller: false, loading: false });
    }
  }, []);

  React.useEffect(() => { refresh(); }, [refresh]);

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
