"use client";

import { useEffect, useRef, useState } from "react";
import {
  deletePlayoutClient,
  fetchPlayoutDevices,
  fetchPlayoutLogs,
  isPlayoutOn,
  startPlayout,
  stopPlayout,
  updatePlayoutClient,
  type PlayoutClient,
  type PlayoutDevice,
} from "@/lib/api";

type Props = {
  open: boolean;
  client: PlayoutClient | null;
  onClose: () => void;
  onSaved: () => void;
};

export default function DecodeSettingsModal({ open, client, onClose, onSaved }: Props) {
  const [name, setName] = useState("");
  const [device, setDevice] = useState("");
  const [formatCode, setFormatCode] = useState("");
  const [deckLinkOut, setDeckLinkOut] = useState(false);
  const [mode, setMode] = useState<"listener" | "caller">("listener");
  const [port, setPort] = useState(9201);
  const [target, setTarget] = useState("");
  const [latency, setLatency] = useState(120);
  const [passphrase, setPassphrase] = useState("");
  const [passDirty, setPassDirty] = useState(false);
  const [devices, setDevices] = useState<PlayoutDevice[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [logsOpen, setLogsOpen] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const logBoxRef = useRef<HTMLPreElement>(null);

  const channelId = client?.id;
  const active = client ? isPlayoutOn(client.status) : false;

  useEffect(() => {
    if (!open || !client || channelId == null) return;
    setName(client.name || `Decode ${channelId}`);
    setDevice(client.device || "");
    setFormatCode(client.format_code || "");
    setDeckLinkOut(!!client.decklink_out);
    setMode(client.mode || "listener");
    setPort(client.port || 9200 + channelId);
    setTarget(client.target || "");
    setLatency(client.latency_ms || 120);
    setPassphrase("");
    setPassDirty(false);
    setError(null);
    setLogsOpen(false);
    setLogs([]);
    fetchPlayoutDevices()
      .then(setDevices)
      .catch((e) => setError(String(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, channelId]);

  useEffect(() => {
    if (!open || channelId == null || !logsOpen) return;
    let alive = true;
    const tick = async () => {
      try {
        const lines = await fetchPlayoutLogs(channelId);
        if (alive) setLogs(lines);
      } catch {
        // ignore
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
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, busy]);

  if (!open || !client) return null;

  const selected = devices.find((d) => d.name === device);
  const formats = selected?.formats ?? [];
  const probeLog = selected?.probe_log || "";

  const refreshDevices = async () => {
    setBusy(true);
    setError(null);
    try {
      const list = await fetchPlayoutDevices(true);
      setDevices(list);
      if (device) {
        const next = list.find((d) => d.name === device);
        if (next?.formats?.length && !next.formats.some((f) => f.code === formatCode)) {
          setFormatCode(next.formats[0].code);
        }
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const buildSaveBody = (): Parameters<typeof updatePlayoutClient>[1] => {
    const selectedDev = devices.find((d) => d.name === device);
    const body: Parameters<typeof updatePlayoutClient>[1] = {
      name: name.trim() || `Decode ${client!.id}`,
      // Keep device/format even when DeckLink out is off — only decklink_out gates open.
      device,
      device_label: selectedDev?.label || selectedDev?.open_name || client?.device_label || "",
      format_code: formatCode,
      decklink_out: deckLinkOut,
      mode,
      port,
      target,
      latency_ms: latency,
    };
    if (passDirty) body.passphrase = passphrase;
    return body;
  };

  const validateForDeckLink = (): string | null => {
    if (deckLinkOut && !device) {
      return "Select a DeckLink output device, or disable DeckLink output";
    }
    if (deckLinkOut && device && !formatCode) {
      return "Select an output format for DeckLink";
    }
    return null;
  };

  const apply = async () => {
    if (active) {
      setError("Stop decode before changing settings");
      return;
    }
    const invalid = validateForDeckLink();
    if (invalid) {
      setError(invalid);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await updatePlayoutClient(client.id, buildSaveBody());
      onSaved();
      onClose();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggleRun = async () => {
    setBusy(true);
    setError(null);
    try {
      if (active) {
        await stopPlayout(client.id);
      } else {
        const invalid = validateForDeckLink();
        if (invalid) {
          setError(invalid);
          return;
        }
        await updatePlayoutClient(client.id, buildSaveBody());
        await startPlayout(client.id);
      }
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (active) {
      setError("Stop decode before deleting");
      return;
    }
    if (!window.confirm(`Delete decode client “${client.name}”?`)) return;
    setBusy(true);
    setError(null);
    try {
      await deletePlayoutClient(client.id);
      onSaved();
      onClose();
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
        aria-label={`Decode settings for ${client.name}`}
      >
        <div className="modal-header">
          <h2>
            <span className="input-badge decode">{client.id}</span>
            <span>{name.trim() || `Decode ${client.id}`}</span>
          </h2>
          <button type="button" className="modal-close" onClick={onClose} aria-label="Close" disabled={busy}>
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        {active && (
          <div className="channel-settings-lock">
            Decode is active — stop it before changing device, format, or SRT settings.
          </div>
        )}

        <div className="channel-settings-form">
          <label className="presets-field">
            <span>Name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} disabled={busy || active} />
          </label>

          <label className="presets-field" style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
            <input
              type="checkbox"
              checked={deckLinkOut}
              onChange={(e) => setDeckLinkOut(e.target.checked)}
              disabled={busy || active}
            />
            <span>Enable DeckLink output</span>
          </label>
          <span className="channel-settings-hint" style={{ marginTop: -8 }}>
            Device selection is kept even when this is off. Turn on only after SRT preview works.
          </span>

          <label className="presets-field">
            <span>DeckLink output</span>
            <div className="channel-settings-actions" style={{ marginTop: 0, marginBottom: 4 }}>
              <select
                value={device}
                onChange={(e) => {
                  setDevice(e.target.value);
                  const next = devices.find((d) => d.name === e.target.value);
                  if (next?.formats?.length) setFormatCode(next.formats[0].code);
                  else setFormatCode("");
                }}
                disabled={busy || active || !deckLinkOut}
                style={{ flex: 1 }}
              >
                <option value="">Select device…</option>
                {devices.map((d) => (
                  <option key={d.name} value={d.name}>
                    {d.label || d.name}
                    {d.formats?.length ? ` (${d.formats.length} modes)` : " (no modes)"}
                  </option>
                ))}
              </select>
              <button type="button" className="badge" onClick={refreshDevices} disabled={busy || active || !deckLinkOut}>
                Re-probe
              </button>
            </div>
            {deckLinkOut && devices.length === 0 && (
              <span className="channel-settings-hint">No devices probed — is FFmpeg+DeckLink available on the host?</span>
            )}
          </label>

          <label className="presets-field">
            <span>Output format</span>
            <select
              value={formats.some((f) => f.code === formatCode) ? formatCode : ""}
              onChange={(e) => setFormatCode(e.target.value)}
              disabled={busy || active || !deckLinkOut || !device || formats.length === 0}
            >
              <option value="">
                {!deckLinkOut ? "Enable DeckLink first" : formats.length ? "Select format…" : "No formats probed"}
              </option>
              {formats.map((f) => (
                <option key={f.code} value={f.code}>
                  {f.label} ({f.code})
                </option>
              ))}
            </select>
            {deckLinkOut && device && formats.length === 0 && (
              <span className="channel-settings-hint">No modes from FFmpeg for this device. Try Re-probe.</span>
            )}
            {probeLog && deckLinkOut && device && formats.length === 0 && (
              <pre className="channel-settings-logbox" style={{ marginTop: 8, maxHeight: 120 }}>
                {probeLog}
              </pre>
            )}
          </label>

          <label className="presets-field">
            <span>SRT mode</span>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as "listener" | "caller")}
              disabled={busy || active}
            >
              <option value="listener">Listener (publishers connect to us)</option>
              <option value="caller">Caller (we pull from a target)</option>
            </select>
          </label>

          {mode === "listener" ? (
            <label className="presets-field">
              <span>Listen port</span>
              <input
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(Number(e.target.value) || 0)}
                disabled={busy || active}
              />
            </label>
          ) : (
            <label className="presets-field">
              <span>Target</span>
              <input
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                disabled={busy || active}
                placeholder="127.0.0.1:9101 or srt://…"
              />
              <span className="channel-settings-hint">
                Same host as encode STREAM: use 127.0.0.1 and the channel port (often 9100+id). Start STREAM first.
              </span>
            </label>
          )}

          <label className="presets-field">
            <span>Latency (ms)</span>
            <input
              type="number"
              min={20}
              max={8000}
              value={latency}
              onChange={(e) => setLatency(Number(e.target.value) || 120)}
              disabled={busy || active}
            />
          </label>

          <label className="presets-field">
            <span>Passphrase {client.has_passphrase && !passDirty ? "(set)" : ""}</span>
            <input
              type="password"
              value={passphrase}
              onChange={(e) => {
                setPassphrase(e.target.value);
                setPassDirty(true);
              }}
              disabled={busy || active}
              placeholder={client.has_passphrase ? "••••••••" : "optional"}
              autoComplete="new-password"
            />
          </label>
        </div>

        <div className="channel-settings-actions">
          <button type="button" className="badge" onClick={remove} disabled={busy || active}>
            Delete
          </button>
          <button
            type="button"
            className={`stream-btn ${active ? "streaming" : "idle"}`}
            onClick={toggleRun}
            disabled={busy}
            title={active ? "Stop decode" : "Save settings and start decode"}
          >
            {busy ? "…" : active ? "STOP" : "START"}
          </button>
          <button type="button" className="badge" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button type="button" className="global-rec-btn" onClick={apply} disabled={busy || active}>
            {busy ? "…" : "Save"}
          </button>
        </div>

        <div className="channel-settings-logs">
          <div className="channel-settings-logs-head">
            <h3>Decode logs</h3>
            <button type="button" className="badge" onClick={() => setLogsOpen((v) => !v)}>
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
