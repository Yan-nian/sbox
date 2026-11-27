import { Server, WifiOff } from "lucide-react";
import { useNode } from "@/contexts/NodeContext";
import { Badge, Button, StatusDot } from "@/components/ui/primitives";
import type { AppRoute } from "@/components/AppShell";

interface NodeScopeBannerProps {
  onNavigate?: (route: AppRoute) => void;
  offlineHint?: string;
}

export function NodeScopeBanner({ onNavigate, offlineHint }: NodeScopeBannerProps) {
  const { activeNode, nodes } = useNode();
  if (!activeNode) return null;

  const isOffline = !activeNode.online;
  const showBanner = nodes.length > 1 || !activeNode.is_local || isOffline;
  if (!showBanner) return null;

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-hairline bg-surface px-3.5 py-2 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <Server className="h-3.5 w-3.5 text-ink-muted" />
          <span className="text-ink-secondary">当前管理节点:</span>
          <span className="font-mono font-semibold text-ink">{activeNode.name}</span>
          <Badge tone={activeNode.is_local ? "accent" : "neutral"}>
            {activeNode.is_local ? "本机" : "远端 Agent"}
          </Badge>
          <Badge tone={activeNode.online ? "ok" : "warn"}>
            <StatusDot state={activeNode.online ? "ok" : "idle"} className="h-1.5 w-1.5" />
            {activeNode.online ? "在线" : "离线"}
          </Badge>
          {activeNode.public_ipv4 && (
            <span className="hidden font-mono text-[11px] text-ink-muted sm:inline">
              {activeNode.public_ipv4}
            </span>
          )}
        </div>

        {onNavigate && (
          <Button variant="ghost" size="xs" onClick={() => onNavigate("/nodes")}>
            切换或管理节点
          </Button>
        )}
      </div>

      {isOffline && (
        <div
          role="status"
          className="flex items-start gap-2.5 rounded-md border border-warn/45 bg-warn/10 px-3.5 py-2.5 text-xs"
        >
          <WifiOff className="mt-0.5 h-4 w-4 shrink-0 text-warn" />
          <div className="min-w-0">
            <p className="font-medium text-warn">
              远程节点 {activeNode.name} 当前处于离线状态
            </p>
            <p className="mt-0.5 text-ink-secondary">
              {offlineHint ||
                "实时控制、核心升降级与日志流暂不可用。该节点上的 sing-box 数据面仍可自治运行，Agent 重连后将自动同步主控配置。"}
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
