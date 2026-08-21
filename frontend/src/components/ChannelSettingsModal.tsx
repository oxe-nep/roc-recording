"use client";

import { useEffect, useRef, useState } from "react";
import {
  fetchSrt,
  fetchStreamLogs,
  setEncodePreset,
  setRecordingCategory,
  setRecordingName,
  startSrt,
  startStream,
  stopSrt,
  stopStream,
  updateSrt,
  type EncodePreset,
  type LibraryCategory,
  type RecordingInfo,
  type SrtInfo,
  type Stream,
  isCaptureOn,
} from "@/lib/api";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";

type Props = {
  open: boolean;
  stream: Stream | null;
  recording: RecordingInfo | null;
  presets: EncodePreset[];
  categories: LibraryCategory[];
  onClose: () => void;
  onSaved: () => void;
};

export default function ChannelSettingsModal({
  open,
  stream,
  recording,
  presets,
  categories,
  onClose,
  onSaved,
}: Props) {
  const [name, setName] = useState("");
  const [category, setCategory] = useState("_unsorted");
  const [preset, setPreset] = useState("");
  const [busy, setBusy] = useState(false);
  const [srtBusy, setSrtBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [srt, setSrt] = useState<SrtInfo | null>(null);
  const [srtMode, setSrtMode] = useState<"listener" | "caller">("listener");
  const [srtPort, setSrtPort] = useState(9101);
  const [srtTarget, setSrtTarget] = useState("");
  const [srtLatency, setSrtLatency] = useState(120);
  const [srtPassphrase, setSrtPassphrase] = useState("");
  const [srtPassDirty, setSrtPassDirty] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const [logsOpen, setLogsOpen] = useState(false);
  const logBoxRef = useRef<HTMLPreElement>(null);
  const baselineRef = useRef({ name: "", category: "", preset: "" });

  useBodyScrollLock(open);

  const isRunning = stream?.status === "running";
  const captureOn = stream ? isCaptureOn(stream.status) : false;
  const isRecording = recording?.status === "recording";
  const channelId = stream?.id;
  const srtStreaming = srt?.status === "streaming";

  // Hydrate once when the modal opens for a channel — not on every 1s poll.
  useEffect(() => {
    if (!open || channelId == null || !stream) return;
    const nextName = recording?.name || `ch${channelId}`;
    const nextCategory = recording?.category || "_unsorted";
    const nextPreset = stream.encode_preset || "";
    setName(nextName);
    setCategory(nextCategory);
    setPreset(nextPreset);
    baselineRef.current = {
      name: nextName,
      category: nextCategory,
      preset: nextPreset,
    };
    setError(null);
    setSrtPassphrase("");
    setSrtPassDirty(false);
    setLogsOpen(false);
    setLogs([]);

    fetchSrt(channelId)
      .then((info) => {
        setSrt(info);
        setSrtMode(info.mode || "listener");
        setSrtPort(info.port || 9100 + channelId);
        setSrtTarget(info.target || "");
        setSrtLatency(info.latency_ms || 120);
      })
      .catch((e) => setError(String(e)));
    // intentionally only when open / channel changes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, channelId]);

  useEffect(() => {
    if (!open || channelId == null) return;
    const tick = () => {
      fetchSrt(channelId)
        .then(setSrt)
        .catch(() => {});
    };
    const interval = setInterval(tick, 2000);
    return () => clearInterval(interval);
  }, [open, channelId]);

  useEffect(() => {
    if (!open || channelId == null || !logsOpen) return;
    let alive = true;
    const tick = async () => {
      try {
        const lines = await fetchStreamLogs(channelId);
        if (alive) setLogs(lines);
      } catch {
        // ignore transient log fetch errors
      }
    };
    tick();
    const interval = setInterval(tick, 1500);
    return () => {
      alive = false;
      clearInterval(interval);
    };
  }, [open, channelId, logsOpen]);

  useEffect(() => {
    if (!logsOpen || !logBoxRef.current) return;
    logBoxRef.current.scrollTop = logBoxRef.current.scrollHeight;
  }, [logs, logsOpen]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy && !srtBusy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, busy, srtBusy]);

  if (!open || !stream) return null;

  const activePreset = presets.find((p) => p.id === preset);

  const apply = async () => {
    if (isRecording) {
      setError("Stop recording before changing name, category, or encode");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const baseline = baselineRef.current;
      const cleanName = name.trim();
      if (cleanName && cleanName !== baseline.name) {
        await setRecordingName(stream.id, cleanName);
      }
      if (category && category !== baseline.category) {
        await setRecordingCategory(stream.id, category);
      }
      if (preset && preset !== baseline.preset) {
        await setEncodePreset(stream.id, preset);
      }

      // Always restart an active capture so encode takes effect now.
      if (captureOn) {
        try {
          await stopSrt(stream.id);
        } catch {
          // ignore if SRT was not running
        }
        await stopStream(stream.id);
        await startStream(stream.id);
      }

      onSaved();
      onClose();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const saveSrt = async () => {
    setSrtBusy(true);
    setError(null);
    try {
      const body: Parameters<typeof updateSrt>[1] = {
        mode: srtMode,
        port: srtPort,
        target: srtTarget,
        latency_ms: srtLatency,
      };
      if (srtPassDirty) body.passphrase = srtPassphrase;
      const info = await updateSrt(stream.id, body);
      setSrt(info);
      setSrtPassDirty(false);
      if (!srtPassphrase) setSrtPassphrase("");
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setSrtBusy(false);
    }
  };

  const toggleSrt = async () => {
    setSrtBusy(true);
    setError(null);
    try {
      if (srtStreaming) {
        setSrt(await stopSrt(stream.id));
      } else {
        // Persist current form values before start.
        const body: Parameters<typeof updateSrt>[1] = {
          mode: srtMode,
          port: srtPort,
          target: srtTarget,
          latency_ms: srtLatency,
        };
        if (srtPassDirty) body.passphrase = srtPassphrase;
        await updateSrt(stream.id, body);
        setSrtPassDirty(false);
        setSrt(await startSrt(stream.id));
      }
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setSrtBusy(false);
    }
  };

  const copyUrl = async () => {
    if (!srt?.publish_url) return;
    try {
      await navigator.clipboard.writeText(srt.publish_url);
    } catch {
      setError("Could not copy URL");
    }
  };

  return (
    <div className="modal-backdrop" onClick={() => !busy && !srtBusy && onClose()} role="presentation">
      <div
        className="modal-panel channel-settings-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label={`Settings for ${stream.name}`}
      >
        <div className="modal-header">
          <h2>
            <span className="input-badge">{stream.id}</span>
            <span>{name.trim() || `ch${stream.id}`}</span>
          </h2>
          <button
            type="button"
            className="modal-close"
            onClick={onClose}
            aria-label="Close"
            disabled={busy || srtBusy}
          >
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        {isRecording && (
          <div className="channel-settings-lock">
            Recording is active — name, category and encode are locked. SRT can still be controlled.
          </div>
        )}

        <div className="channel-settings-form">
          <label className="presets-field">
            <span>Recording name</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={busy || isRecording}
              placeholder={`ch${stream.id}`}
            />
            <span className="channel-settings-hint">
              Files: {(name || `ch${stream.id}`).replace(/\s+/g, "_")}_YYYY-MM-DD_HH-MM-SS.mp4
            </span>
          </label>

          <label className="presets-field">
            <span>Category</span>
            <select
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              disabled={busy || isRecording || categories.length === 0}
            >
              {categories.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name === "_unsorted" ? "Unsorted" : c.name}
                </option>
              ))}
            </select>
            <span className="channel-settings-hint">
              Stored under recordings/{category || "_unsorted"}/
            </span>
          </label>

          <label className="presets-field">
            <span>Encode preset</span>
            <select
              value={preset}
              onChange={(e) => setPreset(e.target.value)}
              disabled={busy || isRecording || presets.length === 0}
            >
              {presets.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
            <span className="channel-settings-hint">
              {activePreset
                ? `${activePreset.video_codec} · ${activePreset.video_bitrate} video · ${activePreset.audio_bitrate} audio`
                : "Choose a preset"}
              {captureOn && !isRecording ? " — Apply restarts capture so encode takes effect" : ""}
            </span>
          </label>
        </div>

        <div className="channel-settings-actions">
          <button type="button" className="badge" onClick={onClose} disabled={busy || srtBusy}>
            Cancel
          </button>
          <button
            type="button"
            className="global-rec-btn"
            onClick={apply}
            disabled={busy || isRecording}
            title={
              isRecording
                ? "Stop recording first"
                : captureOn
                  ? "Save and restart capture so the encode preset applies now"
                  : "Save channel settings"
            }
          >
            {busy ? "…" : captureOn ? "Apply (restart)" : "Apply"}
          </button>
        </div>

        <div className="channel-settings-srt">
          <div className="channel-settings-srt-head">
            <h3>SRT stream</h3>
            <span className={`srt-pill ${srtStreaming ? "on" : ""}`}>
              {srtStreaming ? "ON AIR" : "OFF"}
            </span>
          </div>
          <p className="channel-settings-hint">
            Remuxes the channel master feed (−c copy). Requires FFmpeg built with libsrt. Default
            listener ports: 9100 + channel id.
          </p>

          <div className="channel-settings-form">
            <label className="presets-field">
              <span>Mode</span>
              <select
                value={srtMode}
                onChange={(e) => setSrtMode(e.target.value as "listener" | "caller")}
                disabled={srtBusy || srtStreaming}
              >
                <option value="listener">Listener (clients connect to us)</option>
                <option value="caller">Caller (we push to a target)</option>
              </select>
            </label>

            {srtMode === "listener" ? (
              <label className="presets-field">
                <span>Listen port</span>
                <input
                  type="number"
                  min={1}
                  max={65535}
                  value={srtPort}
                  onChange={(e) => setSrtPort(Number(e.target.value) || 0)}
                  disabled={srtBusy || srtStreaming}
                />
              </label>
            ) : (
              <label className="presets-field">
                <span>Target</span>
                <input
                  value={srtTarget}
                  onChange={(e) => setSrtTarget(e.target.value)}
                  disabled={srtBusy || srtStreaming}
                  placeholder="host:port or srt://…"
                />
              </label>
            )}

            <label className="presets-field">
              <span>Latency (ms)</span>
              <input
                type="number"
                min={20}
                max={8000}
                value={srtLatency}
                onChange={(e) => setSrtLatency(Number(e.target.value) || 120)}
                disabled={srtBusy || srtStreaming}
              />
            </label>

            <label className="presets-field">
              <span>Passphrase {srt?.has_passphrase && !srtPassDirty ? "(set)" : ""}</span>
              <input
                type="password"
                value={srtPassphrase}
                onChange={(e) => {
                  setSrtPassphrase(e.target.value);
                  setSrtPassDirty(true);
                }}
                disabled={srtBusy || srtStreaming}
                placeholder={srt?.has_passphrase ? "••••••••" : "optional"}
                autoComplete="new-password"
              />
              <span className="channel-settings-hint">
                Leave blank and save to clear. Not shown again after save.
              </span>
            </label>
          </div>

          {srt?.publish_url && (
            <div className="srt-url-row">
              <code className="srt-url" title={srt.publish_url}>
                {srt.publish_url}
              </code>
              <button type="button" className="badge" onClick={copyUrl} disabled={!srt.publish_url}>
                Copy
              </button>
            </div>
          )}

          {srt?.error && <div className="error-bar">{srt.error}</div>}

          <div className="channel-settings-actions">
            <button
              type="button"
              className="badge"
              onClick={saveSrt}
              disabled={srtBusy || srtStreaming}
              title={srtStreaming ? "Stop SRT before changing settings" : "Save SRT settings"}
            >
              {srtBusy && !srtStreaming ? "…" : "Save SRT"}
            </button>
            <button
              type="button"
              className={`global-rec-btn ${srtStreaming ? "recording" : ""}`}
              onClick={toggleSrt}
              disabled={srtBusy || (!isRunning && !srtStreaming)}
              title={
                !isRunning && !srtStreaming
                  ? "Start channel before SRT"
                  : srtStreaming
                    ? "Stop SRT"
                    : "Start SRT"
              }
            >
              {srtBusy ? "…" : srtStreaming ? "Stop SRT" : "Start SRT"}
            </button>
          </div>
        </div>

        <div className="channel-settings-logs">
          <div className="channel-settings-logs-head">
            <h3>Channel logs</h3>
            <button
              type="button"
              className="badge"
              onClick={() => setLogsOpen((v) => !v)}
              title={logsOpen ? "Hide channel logs" : "Show recent capture / FFmpeg lines"}
            >
              {logsOpen ? "Hide" : "Show"}
            </button>
          </div>
          {logsOpen && (
            <pre ref={logBoxRef} className="channel-settings-logbox">
              {logs.join("\n")}
            </pre>
          )}
        </div>
      </div>
    </div>
  );
}
