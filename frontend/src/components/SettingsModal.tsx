"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  fetchPlayoutClients,
  fetchRecordingsPath,
  fetchStreams,
  setRecordingsPath,
  updateWorkflow,
  type ChannelWorkflow,
} from "@/lib/api";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";
import { useWorkflows } from "@/hooks/useWorkflows";
import { WORKFLOW_OPTIONS, workflowForChannel } from "@/lib/workflow";
import EncodePresetsEditor from "@/components/EncodePresetsEditor";

type Tab = "storage" | "presets" | "workflows";

type Props = {
  open: boolean;
  onClose: () => void;
  anyRecording?: boolean;
  initialTab?: Tab;
};

export default function SettingsModal({
  open,
  onClose,
  anyRecording = false,
  initialTab = "workflows",
}: Props) {
  const [tab, setTab] = useState<Tab>(initialTab);
  const [pathDraft, setPathDraft] = useState("");
  const [storagePath, setStoragePath] = useState("");
  const [pathBusy, setPathBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [channelIds, setChannelIds] = useState<number[]>([]);
  const [wfBusy, setWfBusy] = useState<number | null>(null);
  const { workflows, reload: reloadWorkflows } = useWorkflows();

  useBodyScrollLock(open);

  const loadStorage = useCallback(async () => {
    const path = await fetchRecordingsPath();
    setStoragePath(path);
    setPathDraft(path);
  }, []);

  const loadChannels = useCallback(async () => {
    const [streams, clients] = await Promise.all([fetchStreams(), fetchPlayoutClients()]);
    const ids = new Set<number>();
    for (const s of streams) ids.add(s.id);
    for (const c of clients) ids.add(c.id);
    setChannelIds(Array.from(ids).sort((a, b) => a - b));
  }, []);

  useEffect(() => {
    if (!open) return;
    setTab(initialTab);
    setError(null);
    void Promise.all([loadStorage(), loadChannels(), reloadWorkflows()]).catch((e) =>
      setError(String(e)),
    );
  }, [open, initialTab, loadStorage, loadChannels, reloadWorkflows]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  const savePath = async () => {
    setPathBusy(true);
    setError(null);
    try {
      const path = await setRecordingsPath(pathDraft.trim());
      setStoragePath(path);
      setPathDraft(path);
      window.dispatchEvent(new Event("roc-library-changed"));
    } catch (e) {
      setError(String(e));
    } finally {
      setPathBusy(false);
    }
  };

  const setWorkflow = async (id: number, workflow: ChannelWorkflow) => {
    setWfBusy(id);
    setError(null);
    try {
      await updateWorkflow(id, workflow);
      await reloadWorkflows();
      window.dispatchEvent(new Event("roc-workflows-changed"));
    } catch (e) {
      setError(String(e));
    } finally {
      setWfBusy(null);
    }
  };

  const tabs = useMemo(
    () =>
      [
        { id: "workflows" as const, label: "Workflows" },
        { id: "storage" as const, label: "Storage" },
        { id: "presets" as const, label: "Encode presets" },
      ] satisfies { id: Tab; label: string }[],
    [],
  );

  if (!open) return null;

  return (
    <div className="modal-backdrop" onClick={onClose} role="presentation">
      <div
        className="modal-panel settings-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label="Settings"
      >
        <div className="modal-header">
          <h2>Settings</h2>
          <button type="button" className="modal-close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>

        <div className="settings-tabs" role="tablist">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={tab === t.id}
              className={`settings-tab${tab === t.id ? " active" : ""}${t.id === "presets" && anyRecording ? " locked" : ""}`}
              onClick={() => {
                if (t.id === "presets" && anyRecording) return;
                setTab(t.id);
              }}
              disabled={t.id === "presets" && anyRecording}
              title={t.id === "presets" && anyRecording ? "Locked while recording" : undefined}
            >
              {t.label}
            </button>
          ))}
        </div>

        {error && <div className="error-message">{error}</div>}

        {tab === "storage" && (
          <div className="settings-tab-panel">
            <p className="settings-tab-intro">
              Absolute path on the capture host where recording category folders are created.
            </p>
            <div className="library-path-bar settings-path-bar">
              <label className="library-path-label" htmlFor="settings-recordings-path">
                Recordings path
              </label>
              <input
                id="settings-recordings-path"
                className="library-path-input"
                value={pathDraft}
                onChange={(e) => setPathDraft(e.target.value)}
                placeholder="/data/recordings"
                disabled={pathBusy}
              />
              <button
                type="button"
                className="badge files-btn"
                onClick={() => void savePath()}
                disabled={pathBusy || !pathDraft.trim() || pathDraft.trim() === storagePath}
              >
                {pathBusy ? "…" : "Save"}
              </button>
            </div>
          </div>
        )}

        {tab === "presets" && (
          <EncodePresetsEditor
            embedded
            open={tab === "presets"}
            onChanged={() => window.dispatchEvent(new Event("roc-presets-changed"))}
          />
        )}

        {tab === "workflows" && (
          <div className="settings-tab-panel">
            <p className="settings-tab-intro">
              Choose what each channel pair focuses on. Only the relevant card row is shown on the
              dashboard.
            </p>
            <div className="workflow-list">
              {channelIds.map((id) => {
                const current = workflowForChannel(workflows, id);
                const busy = wfBusy === id;
                return (
                  <div key={id} className="workflow-row">
                    <div className="workflow-row-head">
                      <span className="input-badge">{id}</span>
                      <span className="workflow-row-title">Channel {id}</span>
                    </div>
                    <div className="workflow-options">
                      {WORKFLOW_OPTIONS.map((opt) => (
                        <label
                          key={opt.id}
                          className={`workflow-option${current === opt.id ? " active" : ""}`}
                        >
                          <input
                            type="radio"
                            name={`workflow-${id}`}
                            value={opt.id}
                            checked={current === opt.id}
                            disabled={busy}
                            onChange={() => void setWorkflow(id, opt.id)}
                          />
                          <span className="workflow-option-text">
                            <strong>{opt.label}</strong>
                            <span className="workflow-option-hint">{opt.hint}</span>
                          </span>
                        </label>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
