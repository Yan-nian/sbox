import { useState } from "react";
import { CloudUpload, Globe, QrCode as QrIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { useNode } from "@/contexts/NodeContext";
import { Badge } from "@/components/ui/primitives";
import { ClientSubTab } from "@/components/subscriptions/ClientSubTab";
import { RemoteSyncTab } from "@/components/subscriptions/RemoteSyncTab";

type SubTab = "client" | "sync";

export function SubscriptionsPage() {
  const [activeTab, setActiveTab] = useState<SubTab>("client");
  const { nodes } = useNode();
  const onlineCount = nodes.filter((n) => n.online).length;

  return (
    <div className="space-y-5">
      {/* Global cluster aggregation header strip */}
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-hairline bg-surface px-3.5 py-2 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <Globe className="h-3.5 w-3.5 text-accent" />
          <span className="font-medium text-ink">全集群多节点订阅聚合引擎</span>
          <Badge tone="accent">
            覆盖全部 {nodes.length || 1} 个节点（{onlineCount || 1} 在线）
          </Badge>
        </div>
        <span className="text-[11px] text-ink-muted">
          成员订阅自动汇总集群内所有已启用节点与多中转入口
        </span>
      </div>

      {/* Segmented tabs */}
      <div className="flex flex-wrap items-center gap-1 border-b border-hairline">
        {(
          [
            { id: "client", label: "全集群客户端订阅分发", icon: QrIcon },
            { id: "sync", label: "远端主动同步 (Gist / Repo / Sub-Store)", icon: CloudUpload },
          ] as const
        ).map((tab) => {
          const Icon = tab.icon;
          const active = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "-mb-px inline-flex items-center gap-2 border-b-2 px-4 py-2.5 text-sm font-medium transition-colors",
                active
                  ? "border-accent text-accent"
                  : "border-transparent text-ink-secondary hover:text-ink",
              )}
            >
              <Icon className="h-4 w-4" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {activeTab === "client" && <ClientSubTab />}
      {activeTab === "sync" && <RemoteSyncTab />}
    </div>
  );
}
