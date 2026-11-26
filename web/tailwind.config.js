/** @type {import('tailwindcss').Config} */
export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "IBM Plex Sans",
          "PingFang SC",
          "Hiragino Sans GB",
          "Microsoft YaHei UI",
          "Segoe UI",
          "system-ui",
          "-apple-system",
          "sans-serif",
        ],
        mono: [
          "JetBrains Mono",
          "Cascadia Code",
          "SF Mono",
          "Fira Code",
          "Consolas",
          "ui-monospace",
          "monospace",
        ],
      },
      colors: {
        canvas: "var(--bg-canvas)",
        surface: "var(--bg-surface)",
        "surface-hover": "var(--bg-surface-hover)",
        recessed: "var(--bg-recessed)",
        hairline: "var(--border-hairline)",
        "hairline-strong": "var(--border-hairline-strong)",
        ink: "var(--text-primary)",
        "ink-secondary": "var(--text-secondary)",
        "ink-muted": "var(--text-muted)",
        accent: "var(--accent-primary)",
        "accent-muted": "var(--accent-primary-muted)",
        uplink: "var(--accent-upload)",
        ok: "var(--signal-ok)",
        warn: "var(--signal-warn)",
        danger: "var(--signal-danger)",
      },
    },
  },
  plugins: [],
};
