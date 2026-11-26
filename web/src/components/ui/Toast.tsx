import React, { createContext, useCallback, useContext, useState } from "react";
import { AlertCircle, CheckCircle2, Info, X } from "lucide-react";
import { cn } from "@/lib/cn";

type ToastType = "success" | "error" | "info";

interface ToastItem {
  id: number;
  type: ToastType;
  title: string;
  detail?: string;
}

interface ToastContextValue {
  push: (type: ToastType, title: string, detail?: string) => void;
  success: (title: string, detail?: string) => void;
  error: (title: string, detail?: string) => void;
  info: (title: string, detail?: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

let seq = 1;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);

  const dismiss = useCallback((id: number) => {
    setItems((prev) => prev.filter((item) => item.id !== id));
  }, []);

  const push = useCallback(
    (type: ToastType, title: string, detail?: string) => {
      const id = seq++;
      setItems((prev) => [...prev.slice(-4), { id, type, title, detail }]);
      window.setTimeout(() => dismiss(id), type === "error" ? 6000 : 3800);
    },
    [dismiss],
  );

  const value: ToastContextValue = {
    push,
    success: (t, d) => push("success", t, d),
    error: (t, d) => push("error", t, d),
    info: (t, d) => push("info", t, d),
  };

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2"
      >
        {items.map((item) => {
          const Icon =
            item.type === "success"
              ? CheckCircle2
              : item.type === "error"
                ? AlertCircle
                : Info;
          return (
            <div
              key={item.id}
              role="status"
              className={cn(
                "pointer-events-auto flex items-start gap-2.5 rounded-md border bg-surface px-3.5 py-2.5 text-sm shadow-lg",
                item.type === "success" && "border-ok/40",
                item.type === "error" && "border-danger/45",
                item.type === "info" && "border-hairline-strong",
              )}
            >
              <Icon
                className={cn(
                  "mt-0.5 h-4 w-4 shrink-0",
                  item.type === "success" && "text-ok",
                  item.type === "error" && "text-danger",
                  item.type === "info" && "text-accent",
                )}
              />
              <div className="min-w-0 flex-1">
                <p className="font-medium text-ink">{item.title}</p>
                {item.detail && (
                  <p className="mt-0.5 break-words text-xs text-ink-secondary">
                    {item.detail}
                  </p>
                )}
              </div>
              <button
                type="button"
                onClick={() => dismiss(item.id)}
                className="text-ink-muted hover:text-ink"
                aria-label="关闭提示"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used within ToastProvider");
  return ctx;
}
