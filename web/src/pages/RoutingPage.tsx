import { useState } from "react";
import { Cloud, GitBranch, Layers } from "lucide-react";
import { cn } from "@/lib/cn";
import { NodeScopeBanner } from "@/components/NodeScopeBanner";
import { UpstreamsProxiesTab } from "@/components/routing/UpstreamsProxiesTab";
import { WarpOutboundsTab } from "@/components/routing/WarpOutboundsTab";
import { VisualRoutingTab } from "@/components/routing/VisualRoutingTab";
import type { AppRoute } from "@/components/AppShell";

type RoutingTab = "upstreams" | "warp" | "rules";

interface RoutingPageProps {
  onNavigate?: (route: AppRoute) => void;
}

export function RoutingPage({ onNavigate }: RoutingPageProps) {
  const [activeTab, setActiveTab] = useState<RoutingTab>("upstreams");
  const [refreshSignal, setRefreshSignal] = useState(0);

  const notifyOutboundsChanged = () => {
    setRefreshSignal((n) => n + 1);
  };

  return (
    <div className="space-y-5">
      <NodeScopeBanner
        onNavigate={onNavigate}
        offlineHint="当前远程节点离线：仍可编辑上游订阅、自定义出站与分流规则（Agent 重连后自动下发），但无法实时拉取 Clash API 策略组测速。"
      />

      {/* Segmented 3-tab navigation bar */}
      <div className="flex flex-wrap items-center gap-1 border-b border-hairline">
        {(
          [
            { id: "upstreams", label: "上游订阅与落地切换", icon: Layers },
            { id: "warp", label: "一键 WARP 与自定义出站", icon: Cloud },
            { id: "rules", label: "可视化分流规则", icon: GitBranch },
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

      {activeTab === "upstreams" && (
        <UpstreamsProxiesTab onOutboundsChanged={notifyOutboundsChanged} />
      )}
      {activeTab === "warp" && (
        <WarpOutboundsTab onOutboundsChanged={notifyOutboundsChanged} />
      )}
      {activeTab === "rules" && (
        <VisualRoutingTab refreshSignal={refreshSignal} onNavigate={onNavigate} />
      )}
    </div>
  );
}
