import { CloudUpload } from "lucide-react";
import { useNode } from "@/contexts/NodeContext";
import { Badge } from "@/components/ui/primitives";
import { RemoteSyncTab } from "@/components/subscriptions/RemoteSyncTab";

export function SubscriptionsPage() {
  const { nodes } = useNode();
  const onlineCount = nodes.filter((n) => n.online).length;

  return (
    <div className="space-y-5">
      {/* Remote Sync Header Strip */}
      <div className="flex flex-wrap items-center justify-between gap-2.5 rounded-lg border border-hairline bg-surface px-4 py-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <CloudUpload className="h-4 w-4 shrink-0 text-accent" />
            <h2 className="text-sm font-semibold text-ink">远端同步</h2>
            <Badge tone="accent" className="font-mono tabular-nums">
              覆盖全部 {nodes.length || 1} 个节点（{onlineCount || 1} 在线）
            </Badge>
          </div>
          <p className="text-xs text-ink-secondary">
            将集群节点自动推送至 Sub-Store、GitHub Gist 或私有仓库，由下游订阅工具统一分发给 Loon / Clash / sing-box 等客户端
          </p>
        </div>
      </div>

      <RemoteSyncTab />
    </div>
  );
}
