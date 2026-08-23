"use client";

import { useCallback, useEffect, useState } from "react";
import { fetchWorkflows } from "@/lib/api";
import { normalizeWorkflow, type ChannelWorkflow } from "@/lib/workflow";

export function useWorkflows() {
  const [workflows, setWorkflows] = useState<Record<number, ChannelWorkflow>>({});
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const raw = await fetchWorkflows();
      const next: Record<number, ChannelWorkflow> = {};
      for (const [key, value] of Object.entries(raw)) {
        const id = Number(key);
        if (Number.isFinite(id)) next[id] = normalizeWorkflow(value);
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
