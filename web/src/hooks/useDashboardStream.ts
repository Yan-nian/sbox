import { useCallback, useEffect, useRef, useState } from "react";
import { api, buildSSEUrl } from "@/lib/api";
import type {
  CoreStatus,
  DashboardStreamPayload,
  MetricsSnapshot,
  SystemInfo,
} from "@/types/api";

export interface ThroughputPoint {
  ts: number;
  up: number;
  down: number;
}

const HISTORY_LENGTH = 60;

function createEmptyHistory(): ThroughputPoint[] {
  const now = Date.now();
  return Array.from({ length: HISTORY_LENGTH }, (_, i) => ({
    ts: now - (HISTORY_LENGTH - 1 - i) * 1000,
    up: 0,
    down: 0,
  }));
}

export interface DashboardLiveState {
  loading: boolean;
  connected: boolean;
  error: string | null;
  core: CoreStatus | null;
  metrics: MetricsSnapshot | null;
  system: SystemInfo | null;
  history: ThroughputPoint[];
  refresh: () => Promise<void>;
}

export function useDashboardStream(enabled: boolean): DashboardLiveState {
  const [loading, setLoading] = useState(true);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [core, setCore] = useState<CoreStatus | null>(null);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [system, setSystem] = useState<SystemInfo | null>(null);
  const [history, setHistory] = useState<ThroughputPoint[]>(() => createEmptyHistory());
  const sourceRef = useRef<EventSource | null>(null);

  const refresh = useCallback(async () => {
    try {
      const data = await api.getDashboardSummary();
      setCore(data.core);
      setMetrics(data.metrics);
      setSystem(data.system);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "加载状态失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    void refresh();

    let closed = false;
    let reconnectTimer: number | null = null;

    const connect = () => {
      if (closed) return;
      const es = new EventSource(buildSSEUrl("/dashboard/stream"));
      sourceRef.current = es;

      es.onopen = () => {
        setConnected(true);
        setError(null);
      };

      const handleMetrics = (ev: MessageEvent) => {
        try {
          const payload = JSON.parse(ev.data) as DashboardStreamPayload;
          if (payload.core) setCore(payload.core);
          if (payload.metrics) {
            setMetrics(payload.metrics);
            setHistory((prev) => [
              ...prev.slice(-(HISTORY_LENGTH - 1)),
              {
                ts: Date.now(),
                up: Math.max(0, payload.metrics.up_speed || 0),
                down: Math.max(0, payload.metrics.down_speed || 0),
              },
            ]);
          }
          setConnected(true);
        } catch {
          // Ignore malformed frame
        }
      };

      es.addEventListener("metrics", handleMetrics);
      es.onmessage = handleMetrics;

      es.onerror = () => {
        setConnected(false);
        es.close();
        if (!closed) {
          reconnectTimer = window.setTimeout(connect, 2500);
        }
      };
    };

    connect();

    return () => {
      closed = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      sourceRef.current?.close();
      sourceRef.current = null;
    };
  }, [enabled, refresh]);

  return { loading, connected, error, core, metrics, system, history, refresh };
}
