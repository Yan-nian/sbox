import React, { useMemo, useState } from "react";
import { formatSpeed, formatTimestamp } from "@/lib/format";
import type { ThroughputPoint } from "@/hooks/useDashboardStream";

interface ThroughputChartProps {
  history: ThroughputPoint[];
}

function niceCeiling(rawMax: number): number {
  const minFloor = 16 * 1024; // 16 KB/s baseline floor
  const target = Math.max(rawMax * 1.15, minFloor);
  const steps = [
    16 * 1024,
    32 * 1024,
    64 * 1024,
    128 * 1024,
    256 * 1024,
    512 * 1024,
    1024 * 1024,
    2 * 1024 * 1024,
    5 * 1024 * 1024,
    10 * 1024 * 1024,
    25 * 1024 * 1024,
    50 * 1024 * 1024,
    100 * 1024 * 1024,
    250 * 1024 * 1024,
    500 * 1024 * 1024,
    1024 * 1024 * 1024,
  ];
  for (const s of steps) {
    if (s >= target) return s;
  }
  return Math.ceil(target / (100 * 1024 * 1024)) * 100 * 1024 * 1024;
}

const VIEW_W = 680;
const VIEW_H = 210;
const PAD_TOP = 14;
const PAD_BOTTOM = 22;
const PAD_LEFT = 6;
const PAD_RIGHT = 6;

export function ThroughputChart({ history }: ThroughputChartProps) {
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  const { ceiling, peakDown, peakUp, downLine, downArea, upLine, upArea, points } =
    useMemo(() => {
      const safe = history.length > 1 ? history : [{ ts: Date.now(), up: 0, down: 0 }, { ts: Date.now(), up: 0, down: 0 }];
      let maxVal = 0;
      let maxDown = 0;
      let maxUp = 0;
      for (const p of safe) {
        if (p.down > maxDown) maxDown = p.down;
        if (p.up > maxUp) maxUp = p.up;
        if (p.down > maxVal) maxVal = p.down;
        if (p.up > maxVal) maxVal = p.up;
      }
      const ceil = niceCeiling(maxVal);
      const plotW = VIEW_W - PAD_LEFT - PAD_RIGHT;
      const plotH = VIEW_H - PAD_TOP - PAD_BOTTOM;
      const baseY = PAD_TOP + plotH;

      const coords = safe.map((p, i) => {
        const x = PAD_LEFT + (i / (safe.length - 1)) * plotW;
        const yDown = baseY - Math.min(1, p.down / ceil) * plotH;
        const yUp = baseY - Math.min(1, p.up / ceil) * plotH;
        return { x, yDown, yUp, raw: p };
      });

      const makeLine = (key: "yDown" | "yUp") =>
        coords
          .map((c, i) => `${i === 0 ? "M" : "L"}${c.x.toFixed(1)},${c[key].toFixed(1)}`)
          .join(" ");

      const makeArea = (line: string) =>
        `${line} L${coords[coords.length - 1].x.toFixed(1)},${baseY.toFixed(1)} L${coords[0].x.toFixed(1)},${baseY.toFixed(1)} Z`;

      const dLine = makeLine("yDown");
      const uLine = makeLine("yUp");

      return {
        ceiling: ceil,
        peakDown: maxDown,
        peakUp: maxUp,
        downLine: dLine,
        downArea: makeArea(dLine),
        upLine: uLine,
        upArea: makeArea(uLine),
        points: coords,
      };
    }, [history]);

  const activePoint =
    hoverIndex !== null && points[hoverIndex]
      ? points[hoverIndex]
      : points[points.length - 1];

  const handleMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (!rect.width) return;
    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    const idx = Math.round(ratio * (points.length - 1));
    setHoverIndex(idx);
  };

  const baseY = VIEW_H - PAD_BOTTOM;
  const midY = PAD_TOP + (VIEW_H - PAD_TOP - PAD_BOTTOM) / 2;

  return (
    <div className="space-y-3">
      {/* Channel readout bar */}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-wrap items-center gap-6">
          <div>
            <div className="flex items-center gap-1.5 text-xs text-ink-secondary">
              <span className="h-2 w-2 rounded-sm bg-accent" />
              <span>下行速率 (RX)</span>
            </div>
            <div className="mt-0.5 font-mono text-2xl font-semibold tracking-tight text-ink tabular-nums">
              {formatSpeed(activePoint?.raw.down)}
            </div>
          </div>

          <div className="h-9 w-px bg-hairline" />

          <div>
            <div className="flex items-center gap-1.5 text-xs text-ink-secondary">
              <span className="h-2 w-2 rounded-sm bg-uplink" />
              <span>上行速率 (TX)</span>
            </div>
            <div className="mt-0.5 font-mono text-2xl font-semibold tracking-tight text-ink tabular-nums">
              {formatSpeed(activePoint?.raw.up)}
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-4 font-mono text-xs text-ink-muted tabular-nums">
          <span>
            60s 峰值: ↓ {formatSpeed(peakDown)} / ↑ {formatSpeed(peakUp)}
          </span>
          {hoverIndex !== null && activePoint && (
            <span className="rounded border border-hairline bg-recessed px-2 py-0.5 text-ink-secondary">
              {formatTimestamp(new Date(activePoint.raw.ts).toISOString())}
            </span>
          )}
        </div>
      </div>

      {/* Recessed oscilloscope well */}
      <div className="relative overflow-hidden rounded-md border border-hairline bg-recessed">
        {/* Y-axis scale labels */}
        <div className="pointer-events-none absolute left-2.5 top-2 z-10 font-mono text-[10px] text-ink-muted tabular-nums">
          {formatSpeed(ceiling)}
        </div>
        <div className="pointer-events-none absolute left-2.5 top-1/2 z-10 -translate-y-2 font-mono text-[10px] text-ink-muted tabular-nums">
          {formatSpeed(ceiling / 2)}
        </div>

        <svg
          viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
          className="block h-52 w-full select-none"
          preserveAspectRatio="none"
          onMouseMove={handleMouseMove}
          onMouseLeave={() => setHoverIndex(null)}
          role="img"
          aria-label="最近 60 秒实时上下行吞吐量曲线图"
        >
          <defs>
            <linearGradient id="rxFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--accent-primary)" stopOpacity="0.26" />
              <stop offset="100%" stopColor="var(--accent-primary)" stopOpacity="0.01" />
            </linearGradient>
            <linearGradient id="txFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--accent-upload)" stopOpacity="0.20" />
              <stop offset="100%" stopColor="var(--accent-upload)" stopOpacity="0.01" />
            </linearGradient>
          </defs>

          {/* Horizontal reference lines */}
          <line
            x1={PAD_LEFT}
            y1={PAD_TOP}
            x2={VIEW_W - PAD_RIGHT}
            y2={PAD_TOP}
            stroke="var(--border-hairline)"
            strokeDasharray="3 4"
            strokeWidth="1"
          />
          <line
            x1={PAD_LEFT}
            y1={midY}
            x2={VIEW_W - PAD_RIGHT}
            y2={midY}
            stroke="var(--border-hairline)"
            strokeDasharray="3 4"
            strokeWidth="1"
          />
          <line
            x1={PAD_LEFT}
            y1={baseY}
            x2={VIEW_W - PAD_RIGHT}
            y2={baseY}
            stroke="var(--border-hairline-strong)"
            strokeWidth="1"
          />

          {/* Area fills */}
          <path d={downArea} fill="url(#rxFill)" />
          <path d={upArea} fill="url(#txFill)" />

          {/* Crisp strokes */}
          <path
            d={downLine}
            fill="none"
            stroke="var(--accent-primary)"
            strokeWidth="2"
            strokeLinejoin="round"
            vectorEffect="non-scaling-stroke"
          />
          <path
            d={upLine}
            fill="none"
            stroke="var(--accent-upload)"
            strokeWidth="1.75"
            strokeLinejoin="round"
            vectorEffect="non-scaling-stroke"
          />

          {/* Hover inspection cursor */}
          {hoverIndex !== null && activePoint && (
            <g>
              <line
                x1={activePoint.x}
                y1={PAD_TOP}
                x2={activePoint.x}
                y2={baseY}
                stroke="var(--text-muted)"
                strokeWidth="1"
                strokeDasharray="2 3"
              />
              <circle
                cx={activePoint.x}
                cy={activePoint.yDown}
                r="3.5"
                fill="var(--accent-primary)"
                stroke="var(--bg-recessed)"
                strokeWidth="1.5"
              />
              <circle
                cx={activePoint.x}
                cy={activePoint.yUp}
                r="3.2"
                fill="var(--accent-upload)"
                stroke="var(--bg-recessed)"
                strokeWidth="1.5"
              />
            </g>
          )}
        </svg>

        <div className="flex items-center justify-between border-t border-hairline px-3 py-1 font-mono text-[10px] text-ink-muted">
          <span>-60s</span>
          <span>-30s</span>
          <span>实时</span>
        </div>
      </div>
    </div>
  );
}
