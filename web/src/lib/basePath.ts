declare global {
  interface Window {
    __PANEL_BASE_PATH__?: string;
  }
}

/**
 * Normalizes a base path to either "" (root) or "/segment" (leading slash, no trailing slash).
 */
export function normalizeBasePath(raw?: string | null): string {
  if (!raw) return "";
  const trimmed = raw.trim();
  if (!trimmed || trimmed === "/") return "";
  const withLeading = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  return withLeading.replace(/\/+$/, "");
}

/**
 * Returns the active secret panel base path injected by Go server
 * (`window.__PANEL_BASE_PATH__`) or "" in local Vite dev mode.
 */
export function getBasePath(): string {
  if (typeof window === "undefined") return "";
  return normalizeBasePath(window.__PANEL_BASE_PATH__ || "");
}

/**
 * Builds a full API v1 URL respecting the active dynamic base path.
 */
export function apiUrl(path: string): string {
  const base = getBasePath();
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${base}/api/v1${suffix}`;
}

/**
 * Extracts the current single-segment SPA route relative to basePath.
 * Example: "/sbox-9f3k2a/core" -> "/core", "/sbox-9f3k2a/" -> "/"
 */
export function getRelativeRoute(pathname: string = window.location.pathname): string {
  const base = getBasePath();
  let rest = pathname;
  if (base && rest.startsWith(base)) {
    rest = rest.slice(base.length);
  }
  if (!rest || rest === "/") return "/";
  const normalized = rest.startsWith("/") ? rest : `/${rest}`;
  return normalized.replace(/\/+$/, "") || "/";
}

/**
 * Builds a browser URL for a relative single-segment route (e.g. "/core" -> "/sbox-9f3k2a/core").
 * Keeping routes strictly 1 segment deep ensures relative `./assets/...` paths always resolve cleanly.
 */
export function buildAppUrl(route: string, overrideBase?: string): string {
  const base = overrideBase !== undefined ? normalizeBasePath(overrideBase) : getBasePath();
  const cleanRoute = route === "/" ? "" : route.startsWith("/") ? route : `/${route}`;
  if (!cleanRoute) {
    return base ? `${base}/` : "/";
  }
  return `${base}${cleanRoute}`;
}
