"use client";

import { useEffect, useRef, useState } from "react";
import {
  deletePlayoutMedia,
  fetchPlayoutMedia,
  uploadPlayoutMedia,
  type PlayoutMediaItem,
} from "@/lib/api";

type Props = {
  open: boolean;
  onClose: () => void;
};

function formatSize(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(0)} KB`;
  return `${n} B`;
}

export default function MediaLibraryModal({ open, onClose }: Props) {
  const [items, setItems] = useState<PlayoutMediaItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const load = async () => {
    try {
      setItems(await fetchPlayoutMedia());
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  };

  useEffect(() => {
    if (!open) return;
    load();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  const onUpload = async (file: File | null) => {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      await uploadPlayoutMedia(file);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
      if (inputRef.current) inputRef.current.value = "";
    }
  };

  const onDelete = async (id: string, name: string) => {
    if (!window.confirm(`Delete “${name}” from the media library?`)) return;
    setBusy(true);
    setError(null);
    try {
      await deletePlayoutMedia(id);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={() => !busy && onClose()} role="presentation">
      <div
        className="modal-panel channel-settings-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label="Media library"
      >
        <div className="modal-header">
          <h2>Media library</h2>
          <button type="button" className="modal-close" onClick={onClose} aria-label="Close" disabled={busy}>
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        <p className="channel-settings-hint">
          Upload external files here. Recorded clips from the Recordings library are available
          directly in each channel&apos;s File mode picker (no upload needed). Supported uploads:
          mp4, mov, mkv, mxf, ts.
        </p>

        <div className="channel-settings-actions" style={{ marginTop: 0 }}>
          <input
            ref={inputRef}
            type="file"
            accept=".mp4,.mov,.mkv,.mxf,.ts,video/*"
            hidden
            onChange={(e) => onUpload(e.target.files?.[0] ?? null)}
          />
          <button type="button" className="global-rec-btn" disabled={busy} onClick={() => inputRef.current?.click()}>
            {busy ? "…" : "Upload"}
          </button>
          <button type="button" className="badge" onClick={load} disabled={busy}>
            Refresh
          </button>
          <button type="button" className="badge" onClick={onClose} disabled={busy}>
            Close
          </button>
        </div>

        {items.length === 0 ? (
          <p className="io-section-empty">No media files yet.</p>
        ) : (
          <ul className="channel-settings-form" style={{ listStyle: "none", padding: 0, marginTop: 16 }}>
            {items.map((it) => (
              <li
                key={it.id}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 12,
                  padding: "8px 0",
                  borderBottom: "1px solid rgba(255,255,255,0.08)",
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div className="card-name" title={it.name}>
                    {it.name}
                  </div>
                  <div className="card-meta">
                    <span className="card-meta-item">{formatSize(it.size)}</span>
                  </div>
                </div>
                <button type="button" className="badge" disabled={busy} onClick={() => onDelete(it.id, it.name)}>
                  Delete
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
