import { useCallback, useEffect, useState } from "react";
import { AlertCircle, Check, Code2, Copy, RefreshCw, Save } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useToast } from "@/components/ui/Toast";
import { Button, Textarea } from "@/components/ui/primitives";

export function CoreConfigEditor() {
  const toast = useToast();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [configJson, setConfigJson] = useState("");
  const [overridePatch, setOverridePatch] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.getCoreConfig();
      setConfigJson(res.config_json || "{}");
      setOverridePatch(res.override_patch || "");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "加载配置失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  const formatPatch = () => {
    const trimmed = overridePatch.trim();
    if (!trimmed) {
      setOverridePatch("");
      setError(null);
      return;
    }
    try {
      const parsed = JSON.parse(trimmed);
      setOverridePatch(JSON.stringify(parsed, null, 2));
      setError(null);
    } catch (err) {
      setError(
        `JSON 语法错误：${err instanceof Error ? err.message : "无法解析补丁对象"}`,
      );
    }
  };

  const handleSave = async () => {
    const trimmed = overridePatch.trim();
    if (trimmed) {
      try {
        const parsed = JSON.parse(trimmed);
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          setError("覆写补丁必须是 JSON 对象（RFC 7396 Merge Patch）。");
          return;
        }
      } catch (err) {
        setError(
          `JSON 语法错误：${err instanceof Error ? err.message : "请检查逗号或引号"}`,
        );
        return;
      }
    }

    setSaving(true);
    setError(null);
    try {
      const res = await api.updateCoreOverride(trimmed);
      if (res.config_json !== undefined) {
        setConfigJson(res.config_json);
      } else {
        await loadConfig();
      }
      toast.success("已保存覆写补丁", "新配置已编译并通过校验");
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "保存覆写补丁失败";
      setError(msg);
      toast.error("覆写校验未通过", msg);
    } finally {
      setSaving(false);
    }
  };

  const copyConfig = async () => {
    try {
      await navigator.clipboard.writeText(configJson);
      setCopied(true);
      toast.info("已复制编译后的 config.json");
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      toast.error("复制失败");
    }
  };

  return (
    <div className="space-y-4">
      {error && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-danger/45 bg-danger/10 p-3 text-xs text-danger"
        >
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <pre className="whitespace-pre-wrap break-all font-mono">{error}</pre>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        {/* Override patch editor */}
        <div className="flex flex-col rounded-md border border-hairline bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline px-3.5 py-2.5">
            <div>
              <h3 className="text-xs font-semibold text-ink">
                自定义 JSON Merge 覆写补丁
              </h3>
              <p className="text-[11px] text-ink-muted">
                按 RFC 7396 深度合并至编译产物；保存前执行 sing-box check 预检
              </p>
            </div>
            <div className="flex items-center gap-1.5">
              <Button variant="ghost" size="xs" onClick={formatPatch}>
                <Code2 className="h-3.5 w-3.5" />
                格式化
              </Button>
              <Button
                variant="primary"
                size="xs"
                loading={saving}
                onClick={() => void handleSave()}
              >
                <Save className="h-3.5 w-3.5" />
                保存覆写
              </Button>
            </div>
          </div>
          <div className="p-3">
            <Textarea
              value={overridePatch}
              onChange={(e) => setOverridePatch(e.target.value)}
              spellCheck={false}
              rows={18}
              placeholder={'{\n  "log": {\n    "level": "info"\n  }\n}'}
              className="font-mono text-xs leading-relaxed"
            />
          </div>
        </div>

        {/* Compiled config readonly preview */}
        <div className="flex flex-col rounded-md border border-hairline bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline px-3.5 py-2.5">
            <div>
              <h3 className="text-xs font-semibold text-ink">
                当前最终编译配置 (config.json)
              </h3>
              <p className="text-[11px] text-ink-muted">
                适配 sing-box 1.12+ 现代规范（只读预览）
              </p>
            </div>
            <div className="flex items-center gap-1.5">
              <Button
                variant="ghost"
                size="xs"
                loading={loading}
                onClick={() => void loadConfig()}
              >
                <RefreshCw className="h-3.5 w-3.5" />
                刷新
              </Button>
              <Button variant="outline" size="xs" onClick={() => void copyConfig()}>
                {copied ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
                复制
              </Button>
            </div>
          </div>
          <div className="flex-1 p-3">
            <pre className="h-[388px] overflow-auto rounded-md border border-hairline bg-recessed p-3 font-mono text-xs leading-relaxed text-ink">
              {loading ? "加载配置中..." : configJson}
            </pre>
          </div>
        </div>
      </div>
    </div>
  );
}
