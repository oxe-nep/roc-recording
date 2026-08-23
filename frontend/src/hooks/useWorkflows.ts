"use client";

import { useCallback, useEffect, useState } from "react";
import { fetchWorkflows } from "@/lib/api";
import { normalizeWorkflowConfig, type ChannelWorkflowConfig } from "@/lib/workflow";

export function useWorkflows() {
  const [workflows, setWorkflows] = useState<Record<number, ChannelWorkflowConfig>>({});
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const raw = await fetchWorkflows();
      const next: Record<number, ChannelWorkflowConfig> = {};
      for (const [key, value] of Object.entries(raw)) {
        const id = Number(key);
        if (Number.isFinite(id)) next[id] = normalizeWorkflowConfig(value);
      }
      setWorkflows(next);
    } catch {
      /* backend may be starting */
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
    const onChange = () => void reload();
    window.addEventListener("roc-workflows-changed", onChange);
    return () => window.removeEventListener("roc-workflows-changed", onChange);
  }, [reload]);

  return { workflows, loading, reload };
}
