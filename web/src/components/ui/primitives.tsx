import React, { useEffect } from "react";
import { Loader2, X } from "lucide-react";
import { cn } from "@/lib/cn";

type ButtonVariant = "primary" | "secondary" | "outline" | "ghost" | "danger";
type ButtonSize = "xs" | "sm" | "md" | "icon";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}

const variantStyles: Record<ButtonVariant, string> = {
  primary:
    "bg-accent text-slate-950 font-medium hover:brightness-110 border border-transparent",
  secondary:
    "bg-surface-hover text-ink border border-hairline hover:border-hairline-strong hover:bg-recessed",
  outline:
    "bg-transparent text-ink border border-hairline hover:border-hairline-strong hover:bg-surface-hover",
  ghost:
    "bg-transparent text-ink-secondary hover:text-ink hover:bg-surface-hover border border-transparent",
  danger:
    "bg-danger/15 text-danger border border-danger/35 hover:bg-danger/25",
};

const sizeStyles: Record<ButtonSize, string> = {
  xs: "h-7 px-2.5 text-xs gap-1.5 rounded",
  sm: "h-8 px-3 text-xs gap-1.5 rounded-md",
  md: "h-9 px-3.5 text-sm gap-2 rounded-md",
  icon: "h-8 w-8 p-0 justify-center rounded-md",
};

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "secondary", size = "md", loading, disabled, children, ...props }, ref) => (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cn(
        "inline-flex items-center justify-center whitespace-nowrap transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60 focus-visible:ring-offset-1 focus-visible:ring-offset-canvas",
        "disabled:opacity-45 disabled:pointer-events-none select-none",
        variantStyles[variant],
        sizeStyles[size],
        className,
      )}
      {...props}
    >
      {loading && <Loader2 className="h-3.5 w-3.5 animate-spin shrink-0" />}
      {children}
    </button>
  ),
);
Button.displayName = "Button";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        "h-9 w-full rounded-md border border-hairline bg-recessed px-3 text-sm text-ink",
        "placeholder:text-ink-muted transition-colors",
        "focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/40",
        "disabled:opacity-50 disabled:cursor-not-allowed",
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = "Input";

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {}

export const Textarea = React.forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, ...props }, ref) => (
    <textarea
      ref={ref}
      className={cn(
        "w-full rounded-md border border-hairline bg-recessed p-3 text-sm text-ink",
        "placeholder:text-ink-muted transition-colors",
        "focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/40",
        "disabled:opacity-50 disabled:cursor-not-allowed",
        className,
      )}
      {...props}
    />
  ),
);
Textarea.displayName = "Textarea";

export interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {}

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        "h-9 w-full rounded-md border border-hairline bg-recessed px-2.5 text-sm text-ink",
        "focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/40",
        "disabled:opacity-50",
        className,
      )}
      {...props}
    />
  ),
);
Select.displayName = "Select";

type BadgeTone = "neutral" | "ok" | "warn" | "danger" | "accent" | "uplink";

const badgeTones: Record<BadgeTone, string> = {
  neutral: "bg-recessed text-ink-secondary border-hairline",
  ok: "bg-ok/12 text-ok border-ok/30",
  warn: "bg-warn/12 text-warn border-warn/30",
  danger: "bg-danger/12 text-danger border-danger/30",
  accent: "bg-accent/12 text-accent border-accent/30",
  uplink: "bg-uplink/12 text-uplink border-uplink/30",
};

export function Badge({
  tone = "neutral",
  className,
  children,
}: {
  tone?: BadgeTone;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs font-medium",
        badgeTones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function StatusDot({
  state,
  className,
}: {
  state: "ok" | "warn" | "danger" | "idle";
  className?: string;
}) {
  const color =
    state === "ok"
      ? "bg-ok animate-led"
      : state === "warn"
        ? "bg-warn animate-led"
        : state === "danger"
          ? "bg-danger"
          : "bg-ink-muted";
  return <span className={cn("inline-block h-2 w-2 rounded-full shrink-0", color, className)} />;
}

export function Panel({
  title,
  description,
  actions,
  className,
  bodyClassName,
  children,
}: {
  title?: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
  bodyClassName?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className={cn(
        "rounded-lg border border-hairline bg-surface",
        className,
      )}
    >
      {(title || actions) && (
        <header className="flex flex-wrap items-center justify-between gap-3 border-b border-hairline px-4 py-3">
          <div className="min-w-0">
            {title && <h2 className="text-sm font-semibold text-ink">{title}</h2>}
            {description && (
              <p className="mt-0.5 text-xs text-ink-secondary">{description}</p>
            )}
          </div>
          {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
        </header>
      )}
      <div className={cn("p-4", bodyClassName)}>{children}</div>
    </section>
  );
}

export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="block text-xs font-medium text-ink-secondary">
        {label}
      </label>
      {children}
      {error ? (
        <p className="text-xs text-danger">{error}</p>
      ) : hint ? (
        <p className="text-xs text-ink-muted">{hint}</p>
      ) : null}
    </div>
  );
}

export function Switch({
  checked,
  disabled,
  onChange,
  label,
}: {
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
  label?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60",
        "disabled:opacity-45 disabled:cursor-not-allowed",
        checked
          ? "border-accent bg-accent"
          : "border-hairline-strong bg-recessed",
      )}
    >
      <span
        className={cn(
          "inline-block h-3.5 w-3.5 rounded-full transition-transform",
          checked ? "translate-x-4 bg-slate-950" : "translate-x-0.5 bg-ink-secondary",
        )}
      />
    </button>
  );
}

export function Modal({
  open,
  onClose,
  title,
  description,
  size = "lg",
  footer,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  size?: "md" | "lg" | "xl";
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const widthClass =
    size === "md" ? "max-w-lg" : size === "xl" ? "max-w-4xl" : "max-w-2xl";

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-6"
      role="dialog"
      aria-modal="true"
    >
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-[1px]"
        onClick={onClose}
      />
      <div
        className={cn(
          "relative z-10 flex max-h-[90vh] w-full flex-col overflow-hidden rounded-lg border border-hairline-strong bg-surface shadow-2xl",
          widthClass,
        )}
      >
        <header className="flex items-start justify-between gap-4 border-b border-hairline px-5 py-3.5">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-ink">{title}</h2>
            {description && (
              <p className="mt-0.5 text-xs text-ink-secondary">{description}</p>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-ink-muted hover:bg-surface-hover hover:text-ink"
            aria-label="关闭窗口"
          >
            <X className="h-4 w-4" />
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>

        {footer && (
          <footer className="flex items-center justify-end gap-2 border-t border-hairline bg-recessed/50 px-5 py-3">
            {footer}
          </footer>
        )}
      </div>
    </div>
  );
}
