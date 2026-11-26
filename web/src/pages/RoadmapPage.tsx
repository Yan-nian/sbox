import { ArrowUpRight, CheckCircle2, Layers } from "lucide-react";
import { Badge, Button, Panel } from "@/components/ui/primitives";
import type { AppRoute } from "@/components/AppShell";

interface RoadmapSpec {
  title: string;
  milestone: string;
  summary: string;
  capabilities: string[];
  tableHint: string;
}

const ROADMAP_SPECS: Record<
  Extract<AppRoute, "/routing" | "/sync">,
  RoadmapSpec
> = {
  "/routing": {
    title: "外部订阅出站、一键 WARP 与可视化分流",
    milestone: "Milestone 3",
    summary:
      "聚合外部上游订阅节点与 Cloudflare WARP 落地，结合官方 .srs 远程规则集实现零代码策略分流。",
    capabilities: [
      "自适应解析 Base64 URI、Clash (Mihomo) YAML、sing-box JSON 上游订阅并定时同步",
      "自动生成 urltest 与 selector 出站组，支持面板实时测速与手动切换落地节点",
      "一键注册 Cloudflare WARP 并编译为 sing-box 1.12+ 原生 WireGuard endpoints",
      "AI 服务 / 流媒体 / BT 安全拦截预设分流卡片与自定义规则排序",
    ],
    tableHint: "SQLite 表 `upstream_subs`、`outbounds_extra` 与 `route_rules` 已完成建表。",
  },
  "/sync": {
    title: "多格式订阅分发、远端同步与加密备份",
    milestone: "Milestone 4",
    summary:
      "面向个人与多台 VPS 场景的一站式订阅渲染、防覆盖远端推送以及 AES-256-GCM 加密状态备份。",
    capabilities: [
      "内置 /sub/{token} 接口，按 User-Agent 自适应返回 Base64 URI / sing-box JSON / Clash YAML",
      "GitHub Gist / 仓库同步：支持单机独立文件及按 instance_id 区块无损增量合并",
      "Sub-Store 后端 REST API 幂等推送，多台 VPS 变更经 3 秒防抖自动汇总",
      "SQLite 一致性快照 + 证书私钥 AES-256-GCM 加密打包定时备份至私有仓库",
    ],
    tableHint: "SQLite 表 `sync_targets` 已完成建表。",
  },
};

interface RoadmapPageProps {
  route: Extract<AppRoute, "/routing" | "/sync">;
  onNavigate: (route: AppRoute) => void;
}

export function RoadmapPage({ route, onNavigate }: RoadmapPageProps) {
  const spec = ROADMAP_SPECS[route];

  return (
    <div className="max-w-3xl space-y-5">
      <Panel
        title={
          <span className="flex items-center gap-2.5">
            <span>{spec.title}</span>
            <Badge tone="accent">{spec.milestone} 规划模块</Badge>
          </span>
        }
        description={spec.summary}
      >
        <div className="space-y-4">
          <div className="rounded-md border border-hairline bg-recessed p-4">
            <div className="mb-2.5 flex items-center gap-1.5 text-xs font-medium text-ink">
              <Layers className="h-3.5 w-3.5 text-accent" />
              计划交付能力
            </div>
            <ul className="space-y-2 text-xs text-ink-secondary">
              {spec.capabilities.map((cap) => (
                <li key={cap} className="flex items-start gap-2">
                  <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-muted" />
                  <span>{cap}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline pt-4 text-xs">
            <span className="font-mono text-ink-muted">{spec.tableHint}</span>
            <Button variant="secondary" size="sm" onClick={() => onNavigate("/core")}>
              通过 JSON 覆写补丁提前配置
              <ArrowUpRight className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </Panel>
    </div>
  );
}
