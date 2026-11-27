/**
 * Copy text to the system clipboard.
 * Uses the modern Async Clipboard API when available in a secure context,
 * and falls back to an offscreen <textarea> + document.execCommand("copy")
 * so copy buttons continue to work over plain HTTP (e.g. http://<vps-ip>:18080).
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false;

  if (
    typeof navigator !== "undefined" &&
    navigator.clipboard &&
    typeof window !== "undefined" &&
    window.isSecureContext
  ) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Fall through to legacy execCommand fallback
    }
  }

  if (typeof document === "undefined" || !document.body) {
    return false;
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.setAttribute("aria-hidden", "true");
  textarea.style.position = "fixed";
  textarea.style.top = "-9999px";
  textarea.style.left = "-9999px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";

  const previousFocus =
    document.activeElement instanceof HTMLElement ? document.activeElement : null;

  document.body.appendChild(textarea);
  try {
    textarea.focus({ preventScroll: true });
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    return Boolean(document.execCommand("copy"));
  } catch {
    return false;
  } finally {
    document.body.removeChild(textarea);
    previousFocus?.focus({ preventScroll: true });
  }
}
