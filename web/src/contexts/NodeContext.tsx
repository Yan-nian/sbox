import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { api } from "@/lib/api";
import type { NodeView } from "@/types/api";

const STORAGE_KEY = "sbox_panel_active_node_id";

interface NodeContextValue {
  nodes: NodeView[];
  activeNodeId: number;
  activeNode: NodeView | null;
  loading: boolean;
  setActiveNodeId: (id: number) => void;
  refreshNodes: () => Promise<NodeView[]>;
}

const NodeContext = createContext<NodeContextValue | null>(null);

function readInitialNodeId(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = Number.parseInt(raw, 10);
      if (Number.isFinite(parsed) && parsed > 0) return parsed;
    }
  } catch {
    // Ignore storage errors
  }
  return 1;
}

export function NodeProvider({ children }: { children: React.ReactNode }) {
  const [nodes, setNodes] = useState<NodeView[]>([]);
  const [activeNodeId, setActiveNodeIdState] = useState<number>(() => readInitialNodeId());
  const [loading, setLoading] = useState(true);

  const setActiveNodeId = useCallback((id: number) => {
    const next = Number.isFinite(id) && id > 0 ? id : 1;
    setActiveNodeIdState(next);
    try {
      localStorage.setItem(STORAGE_KEY, String(next));
    } catch {
      // Ignore storage errors
    }
  }, []);

  const refreshNodes = useCallback(async (): Promise<NodeView[]> => {
    try {
      const res = await api.listNodes();
      const list = Array.isArray(res.nodes) ? res.nodes : [];
      setNodes(list);
      if (list.length > 0) {
        setActiveNodeIdState((curr) => {
          if (list.some((n) => n.id === curr)) return curr;
          const fallback = list.find((n) => n.is_local)?.id ?? list[0].id;
          try {
            localStorage.setItem(STORAGE_KEY, String(fallback));
          } catch {
            // Ignore storage errors
          }
          return fallback;
        });
      }
      return list;
    } catch {
      return [];
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshNodes();
    const timer = window.setInterval(() => {
      void refreshNodes();
    }, 10_000);
    return () => window.clearInterval(timer);
  }, [refreshNodes]);

  const activeNode = useMemo(() => {
    if (nodes.length === 0) return null;
    return nodes.find((n) => n.id === activeNodeId) ?? nodes.find((n) => n.is_local) ?? nodes[0];
  }, [nodes, activeNodeId]);

  const value = useMemo<NodeContextValue>(
    () => ({
      nodes,
      activeNodeId: activeNode?.id ?? activeNodeId,
      activeNode,
      loading,
      setActiveNodeId,
      refreshNodes,
    }),
    [nodes, activeNode, activeNodeId, loading, setActiveNodeId, refreshNodes],
  );

  return <NodeContext.Provider value={value}>{children}</NodeContext.Provider>;
}

export function useNode(): NodeContextValue {
  const ctx = useContext(NodeContext);
  if (!ctx) throw new Error("useNode must be used within NodeProvider");
  return ctx;
}
