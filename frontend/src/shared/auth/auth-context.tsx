import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { apiRequest, readToken, writeToken } from "@/shared/api/client";

export type AdminProfile = { username: string; role: string };

type AuthContextValue = {
  admin: AdminProfile | null;
  ready: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [admin, setAdmin] = useState<AdminProfile | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    async function restore(): Promise<void> {
      if (!readToken()) {
        setReady(true);
        return;
      }
      try {
        const profile = await apiRequest<AdminProfile>("/admin/api/auth/me");
        if (!cancelled) setAdmin(profile);
      } catch {
        writeToken("");
      } finally {
        if (!cancelled) setReady(true);
      }
    }
    void restore();
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const result = await apiRequest<{ token: string; username: string; role: string }>(
      "/admin/api/auth/login",
      { method: "POST", body: { username, password }, auth: false },
    );
    writeToken(result.token);
    setAdmin({ username: result.username, role: result.role });
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiRequest("/admin/api/auth/logout", { method: "POST" });
    } catch {
      /* ignore */
    }
    writeToken("");
    setAdmin(null);
  }, []);

  const changePassword = useCallback(async (currentPassword: string, newPassword: string) => {
    await apiRequest("/admin/api/auth/password", {
      method: "POST",
      body: { currentPassword, newPassword },
    });
    writeToken("");
    setAdmin(null);
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ admin, ready, login, logout, changePassword }),
    [admin, ready, login, logout, changePassword],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth must be used inside AuthProvider");
  return value;
}
