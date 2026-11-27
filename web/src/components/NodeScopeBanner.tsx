import { WifiOff } from "lucide-react";
import { useNode } from "@/contexts/NodeContext";
import { Button } from "@/components/ui/primitives";
import type { AppRoute } from "@/components/AppShell";

interface NodeScopeBannerProps {
  onNavigate?: (route: AppRoute) => void;
  offlineHint?: string;
}

export function NodeScopeBanner({ onNavigate, offlineHint }: NodeScopeBannerProps) {
  const { activeNode } = useNode();
  if (!activeNode || activeNode.online) return null;

  return (
    <div
      role="status"
      className="flex flex-wrap items-start justify-between gap-3 rounded-md border border-warn/45 bg-warn/10 px-3.5 py-2.5 text-xs"
    >
      <div className="flex min-w-0 items-start gap-2.5">
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

      {onNavigate && (
        <Button variant="ghost" size="xs" onClick={() => onNavigate("/")}>
          返回主控集群
        </Button>
      )}
    </div>
  );
}
