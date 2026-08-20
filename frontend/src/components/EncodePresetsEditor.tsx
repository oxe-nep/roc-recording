"use client";

import { useCallback, useEffect, useState } from "react";
import {
  createEncodePreset,
  deleteEncodePreset,
  fetchEncodePresets,
  updateEncodePreset,
  type EncodePreset,
} from "@/lib/api";

const emptyForm = (): EncodePreset => ({
  id: "",
  label: "",
  video_codec: "h264_nvenc",
  video_bitrate: "12M",
  video_maxrate: "14M",
  video_bufsize: "20M",
  video_preset: "p4",
  video_gop: 50,
  audio_bitrate: "192k",
});

type Props = {
  open: boolean;
  onClose: () => void;
  onChanged?: () => void;
};

export default function EncodePresetsEditor({ open, onClose, onChanged }: Props) {
  const [presets, setPresets] = useState<EncodePreset[]>([]);
  const [form, setForm] = useState<EncodePreset>(emptyForm());
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setPresets(await fetchEncodePresets());
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    if (open) {
      load();
      setEditingId(null);
      setForm(emptyForm());
    }
  }, [open, load]);

  if (!open) return null;

  const startCreate = () => {
    setEditingId(null);
    setForm(emptyForm());
  };

  const startEdit = (p: EncodePreset) => {
    setEditingId(p.id);
    setForm({ ...p });
  };

  const save = async () => {
    setBusy(true);
    try {
      if (editingId) {
        const { id: _id, ...rest } = form;
        await updateEncodePreset(editingId, rest);
      } else {
        if (!form.id.trim()) throw new Error("Preset id is required");
        await createEncodePreset(form);
      }
      await load();
      onChanged?.();
      startCreate();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm(`Delete preset "${id}"? Channels using it will fall back to default.`)) return;
    setBusy(true);
    try {
      await deleteEncodePreset(id);
      await load();
      onChanged?.();
      if (editingId === id) startCreate();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const setField = <K extends keyof EncodePreset>(key: K, value: EncodePreset[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="presentation">
      <div
        className="modal-panel presets-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label="Encode presets"
      >
        <div className="modal-header">
          <h2>Encode presets</h2>
          <button type="button" className="modal-close" onClick={onClose}>
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        <div className="presets-layout">
          <div className="presets-list">
            <button type="button" className="badge files-btn" onClick={startCreate} disabled={busy}>
              + New
            </button>
            {presets.map((p) => (
              <div key={p.id} className={`presets-row ${editingId === p.id ? "active" : ""}`}>
                <button type="button" className="presets-row-main" onClick={() => startEdit(p)}>
                  <span className="presets-row-label">{p.label}</span>
                  <span className="presets-row-meta">
                    {p.id} · {p.video_bitrate} · {p.audio_bitrate}
                  </span>
                </button>
                <button type="button" className="badge delete-btn" onClick={() => remove(p.id)} disabled={busy}>
                  Delete
                </button>
              </div>
            ))}
          </div>

          <div className="presets-form">
            <div className="presets-form-title">{editingId ? `Edit ${editingId}` : "New preset"}</div>
            {!editingId && (
              <label className="presets-field">
                <span>ID</span>
                <input
                  value={form.id}
                  onChange={(e) => setField("id", e.target.value)}
                  placeholder="hq"
                  disabled={busy}
                />
              </label>
            )}
            <label className="presets-field">
              <span>Label</span>
              <input
                value={form.label}
                onChange={(e) => setField("label", e.target.value)}
                placeholder="HQ 12 Mbit"
                disabled={busy}
              />
            </label>
            <label className="presets-field">
              <span>Video codec</span>
              <input
                value={form.video_codec}
                onChange={(e) => setField("video_codec", e.target.value)}
                disabled={busy}
              />
            </label>
            <div className="presets-grid">
              <label className="presets-field">
                <span>Video bitrate</span>
                <input
                  value={form.video_bitrate}
                  onChange={(e) => setField("video_bitrate", e.target.value)}
                  placeholder="12M"
                  disabled={busy}
                />
              </label>
              <label className="presets-field">
                <span>Maxrate</span>
                <input
                  value={form.video_maxrate}
                  onChange={(e) => setField("video_maxrate", e.target.value)}
                  placeholder="14M"
                  disabled={busy}
                />
              </label>
              <label className="presets-field">
                <span>Bufsize</span>
                <input
                  value={form.video_bufsize}
                  onChange={(e) => setField("video_bufsize", e.target.value)}
                  placeholder="20M"
                  disabled={busy}
                />
              </label>
              <label className="presets-field">
                <span>NVENC preset</span>
                <input
                  value={form.video_preset}
                  onChange={(e) => setField("video_preset", e.target.value)}
                  placeholder="p4"
                  disabled={busy}
                />
              </label>
              <label className="presets-field">
                <span>GOP</span>
                <input
                  type="number"
                  value={form.video_gop}
                  onChange={(e) => setField("video_gop", Number(e.target.value) || 50)}
                  disabled={busy}
                />
              </label>
              <label className="presets-field">
                <span>Audio bitrate</span>
                <input
                  value={form.audio_bitrate}
                  onChange={(e) => setField("audio_bitrate", e.target.value)}
                  placeholder="192k"
                  disabled={busy}
                />
              </label>
            </div>
            <p className="presets-hint">
              Saving updates the always-on master encode. Channels already using this preset restart
              capture to apply changes.
            </p>
            <div className="presets-form-actions">
              <button type="button" className="global-rec-btn" onClick={save} disabled={busy}>
                {busy ? "…" : editingId ? "Save changes" : "Create preset"}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
