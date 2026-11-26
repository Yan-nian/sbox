import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { api, setAuthToken, setUnauthorizedHandler } from "@/lib/api";
import type { AuthStatusResponse } from "@/types/api";

interface AuthContextValue {
  loading: boolean;
  error: string | null;
  status: AuthStatusResponse | null;
  refresh: () => Promise<AuthStatusResponse | null>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<AuthStatusResponse | null>(null);

  const refresh = useCallback(async () => {
    try {
      setError(null);
      const data = await api.getAuthStatus();
      setStatus(data);
      return data;
    } catch (err) {
      const message = err instanceof Error ? err.message : "无法连接到面板后端";
      setError(message);
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // Ignore logout network errors; clear client session regardless
    } finally {
      setAuthToken(null);
      setStatus((prev) => (prev ? { ...prev, authenticated: false } : prev));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setStatus((prev) => (prev ? { ...prev, authenticated: false } : prev));
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  return (
    <AuthContext.Provider value={{ loading, error, status, refresh, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
