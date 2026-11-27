import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownToLine, Pause, Play, Search, Trash2 } from "lucide-react";
import { api, buildSSEUrl } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatTimestamp } from "@/lib/format";
import { Badge, Button, Input, StatusDot } from "@/components/ui/primitives";
import type { LogEntry } from "@/types/api";

const MAX_LOG_LINES = 800;

const LEVELS = ["ALL", "ERROR", "WARN", "INFO", "DEBUG"] as const;
type LevelFilter = (typeof LEVELS)[number];

function normalizeLevel(raw: string): string {
  const upper = (raw || "INFO").toUpperCase();
  if (upper.includes("FATAL") || upper.includes("PANIC") || upper.includes("ERR")) return "ERROR";
  if (upper.includes("WARN")) return "WARN";
  if (upper.includes("DEBUG") || upper.includes("TRACE")) return "DEBUG";
  return "INFO";
}

function levelColor(level: string): string {
  switch (normalizeLevel(level)) {
    case "ERROR":
      return "text-danger";
    case "WARN":
      return "text-warn";
    case "DEBUG":
      return "text-ink-muted";
    default:
      return "text-accent";
  }
}

interface LogTerminalProps {
  nodeId: number;
  offline?: boolean;
}

export function LogTerminal({ nodeId, offline = false }: LogTerminalProps) {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [paused, setPaused] = useState(false);
  const [levelFilter, setLevelFilter] = useState<LevelFilter>("ALL");
  const [keyword, setKeyword] = useState("");
  const [autoScroll, setAutoScroll] = useState(true);

  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const viewportRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let active = true;
    setLogs([]);
    setConnected(false);

    if (offline) return;

    api
      .getRecentLogs(200, nodeId)
      .then((res) => {
        if (active && Array.isArray(res.logs)) {
          setLogs(res.logs.slice(-MAX_LOG_LINES));
        }
      })
      .catch(() => {
        // SSE stream will still populate live logs
      });

    let reconnectTimer: number | null = null;
    let source: EventSource | null = null;

    const connect = () => {
      if (!active) return;
      source = new EventSource(buildSSEUrl("/logs/stream", nodeId));
      source.onopen = () => setConnected(true);

      const handleLog = (ev: MessageEvent) => {
        if (pausedRef.current) return;
        try {
          const entry = JSON.parse(ev.data) as LogEntry;
          setLogs((prev) => [...prev.slice(-(MAX_LOG_LINES - 1)), entry]);
        } catch {
          // Ignore malformed frame
        }
      };

      source.addEventListener("log", handleLog);
      source.onmessage = handleLog;

      source.onerror = () => {
        setConnected(false);
        source?.close();
        if (active) {
          reconnectTimer = window.setTimeout(connect, 2500);
        }
      };
    };

    connect();

    return () => {
      active = false;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      source?.close();
    };
  }, [nodeId, offline]);

  const filtered = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return logs.filter((entry) => {
      if (levelFilter !== "ALL" && normalizeLevel(entry.level) !== levelFilter) {
        return false;
      }
      if (q) {
        const hay = `${entry.source} ${entry.level} ${entry.message}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [logs, levelFilter, keyword]);

  useEffect(() => {
    if (!autoScroll || !viewportRef.current) return;
    viewportRef.current.scrollTop = viewportRef.current.scrollHeight;
  }, [filtered, autoScroll]);

  const handleScroll = () => {
    const el = viewportRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 36;
    if (atBottom !== autoScroll) {
      setAutoScroll(atBottom);
    }
  };

  return (
    <div className="space-y-3">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <div className="flex flex-wrap items-center gap-1.5">
          {LEVELS.map((lvl) => (
            <button
              key={lvl}
              type="button"
              onClick={() => setLevelFilter(lvl)}
              className={cn(
                "rounded border px-2 py-1 font-mono text-[11px] transition-colors",
                levelFilter === lvl
                  ? "border-accent/50 bg-accent-muted text-accent"
                  : "border-hairline bg-recessed text-ink-secondary hover:text-ink",
              )}
            >
              {lvl === "ALL" ? "全部级别" : lvl}
            </button>
          ))}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="relative w-52">
            <Search className="pointer-events-none absolute left-2.5 top-2.5 h-3.5 w-3.5 text-ink-muted" />
            <Input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder="过滤关键词或来源..."
              className="h-8 pl-8 text-xs"
            />
          </div>

          <Button
            variant={paused ? "primary" : "secondary"}
            size="sm"
            disabled={offline}
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
            {paused ? "恢复接收" : "暂停"}
          </Button>

          <Button variant="outline" size="sm" onClick={() => setLogs([])}>
            <Trash2 className="h-3.5 w-3.5" />
            清空
          </Button>

          <Badge tone={offline ? "neutral" : connected ? "ok" : "warn"}>
            <StatusDot
              state={offline ? "idle" : connected ? "ok" : "warn"}
              className="h-1.5 w-1.5"
            />
            {offline ? "节点离线" : connected ? "实时流" : "重连中"}
          </Badge>
        </div>
      </div>

      {/* Terminal well */}
      <div className="relative overflow-hidden rounded-md border border-hairline bg-recessed">
        <div
          ref={viewportRef}
          onScroll={handleScroll}
          className="h-[460px] overflow-y-auto p-3 font-mono text-xs leading-relaxed"
        >
          {offline ? (
            <div className="flex h-full items-center justify-center text-ink-muted">
              当前远程节点离线，Agent 重连后将自动恢复实时日志推送
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex h-full items-center justify-center text-ink-muted">
              {logs.length === 0 ? "等待日志流推送..." : "没有匹配当前筛选条件的日志记录"}
            </div>
          ) : (
            <div className="space-y-0.5">
              {filtered.map((entry, idx) => (
                <div
                  key={`${entry.time}-${idx}`}
                  className="grid grid-cols-[68px_52px_84px_1fr] gap-2 rounded px-1.5 py-0.5 hover:bg-surface/60"
                >
                  <span className="text-ink-muted tabular-nums">
                    {formatTimestamp(entry.time)}
                  </span>
                  <span className={cn("font-semibold", levelColor(entry.level))}>
                    {normalizeLevel(entry.level)}
                  </span>
                  <span className="truncate text-ink-secondary" title={entry.source}>
                    [{entry.source || "core"}]
                  </span>
                  <span className="break-all text-ink">{entry.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        {!autoScroll && (
          <button
            type="button"
            onClick={() => {
              setAutoScroll(true);
              if (viewportRef.current) {
                viewportRef.current.scrollTop = viewportRef.current.scrollHeight;
              }
            }}
            className="absolute bottom-3 right-4 inline-flex items-center gap-1.5 rounded-md border border-hairline-strong bg-surface px-2.5 py-1 text-xs text-ink shadow-md hover:border-accent"
          >
            <ArrowDownToLine className="h-3.5 w-3.5 text-accent" />
            滚动到最新
          </button>
        )}
      </div>

      <div className="flex items-center justify-between text-[11px] text-ink-muted tabular-nums">
        <span>
          显示 {filtered.length} / 缓冲 {logs.length} 条（最多保留 {MAX_LOG_LINES} 条）
        </span>
        <span>{paused ? "已暂停追加新日志" : "自动跟随最新日志输出"}</span>
      </div>
    </div>
  );
}
