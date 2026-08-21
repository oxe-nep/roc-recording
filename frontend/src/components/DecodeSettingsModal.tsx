"use client";

import { useEffect, useRef, useState } from "react";
import {
  encodeLibraryPlayRef,
  fetchPlayoutDevices,
  fetchPlayoutLogs,
  fetchPlayoutMedia,
  isPlayoutOn,
  startPlayout,
  stopPlayout,
  updatePlayoutClient,
  type LibraryFile,
  type PlayoutClient,
  type PlayoutDevice,
  type PlayoutMediaItem,
} from "@/lib/api";
import LibraryModal from "@/components/LibraryModal";

type Props = {
  open: boolean;
  client: PlayoutClient | null;
  onClose: () => void;
  onSaved: () => void;
};

function displayFileLabel(fileId: string, fileName?: string, media?: PlayoutMediaItem[]): string {
  if (fileName) return fileName;
  if (!fileId) return "";
  const uploaded = media?.find((m) => m.id === fileId);
  if (uploaded) return uploaded.name;
  return fileId.startsWith("lib:") ? fileId.slice(4) : fileId;
}

export default function DecodeSettingsModal({ open, client, onClose, onSaved }: Props) {
  const [name, setName] = useState("");
  const [formatCode, setFormatCode] = useState("");
  const [source, setSource] = useState<"srt" | "file">("srt");
  const [fileId, setFileId] = useState("");
  const [fileLabel, setFileLabel] = useState("");
  const [loop, setLoop] = useState(false);
  const [mode, setMode] = useState<"listener" | "caller">("caller");
  const [port, setPort] = useState(9201);
  const [target, setTarget] = useState("");
  const [latency, setLatency] = useState(120);
  const [passphrase, setPassphrase] = useState("");
  const [passDirty, setPassDirty] = useState(false);
  const [devices, setDevices] = useState<PlayoutDevice[]>([]);
  const [media, setMedia] = useState<PlayoutMediaItem[]>([]);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [logsOpen, setLogsOpen] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const logBoxRef = useRef<HTMLPreElement>(null);

  const channelId = client?.id;
  const active = client ? isPlayoutOn(client.status) : false;
  const deviceName = client?.device || "";
  const deviceLabel = client?.device_label || deviceName;

  useEffect(() => {
    if (!open || !client || channelId == null) return;
    setName(client.name || `Decode ${channelId}`);
    setFormatCode(client.format_code || "");
    setSource(client.source === "file" ? "file" : "srt");
    setFileId(client.file_id || "");
    setFileLabel(displayFileLabel(client.file_id || "", client.file_name));
    setLoop(!!client.loop);
    setMode(client.mode || "caller");
    setPort(client.port || 9200 + channelId);
    setTarget(client.target || "");
    setLatency(client.latency_ms || 120);
    setPassphrase("");
    setPassDirty(false);
    setError(null);
    setLogsOpen(false);
    setLogs([]);
    setLibraryOpen(false);
    Promise.all([fetchPlayoutDevices(), fetchPlayoutMedia()])
      .then(([devs, files]) => {
        setDevices(devs);
        setMedia(files);
        setFileLabel((prev) => prev || displayFileLabel(client.file_id || "", client.file_name, files));
      })
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
      if (e.key === "Escape" && !busy && !libraryOpen) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, busy, libraryOpen]);

  if (!open || !client) return null;

  const selected =
    devices.find((d) => d.name === deviceName) ||
    devices.find((d) => d.label === deviceLabel) ||
    devices.find((d) => (client.device_label && d.label === client.device_label) || false);
  const formats = selected?.formats ?? [];
  const probeLog = selected?.probe_log || "";

  const refreshDevices = async () => {
    setBusy(true);
    setError(null);
    try {
      const list = await fetchPlayoutDevices(true);
      setDevices(list);
      const next =
        list.find((d) => d.name === deviceName) || list.find((d) => d.label === deviceLabel);
      if (next?.formats?.length && !next.formats.some((f) => f.code === formatCode)) {
        setFormatCode(next.formats[0].code);
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const buildSaveBody = (): Parameters<typeof updatePlayoutClient>[1] => {
    const body: Parameters<typeof updatePlayoutClient>[1] = {
      name: name.trim() || `Decode ${client!.id}`,
      format_code: formatCode,
      decklink_out: true,
      source,
      file_id: fileId,
      loop,
      mode,
      port,
      target,
      latency_ms: latency,
    };
    if (passDirty) body.passphrase = passphrase;
    return body;
  };

  const validate = (): string | null => {
    if (!formatCode) return "Select an output format";
    if (source === "file" && !fileId) return "Select a recording or media file";
    if (source === "srt" && mode === "caller" && !target.trim()) return "Caller mode requires a target";
    return null;
  };

  const apply = async () => {
    if (active) {
      setError("Stop decode before changing settings");
      return;
    }
    const invalid = validate();
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
        const invalid = validate();
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

  const onPickRecording = (f: LibraryFile) => {
    const id = encodeLibraryPlayRef(f.category, f.name);
    setFileId(id);
    setFileLabel(`${f.category}/${f.name}`);
  };

  return (
    <>
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
              Decode is active — stop it before changing format, source, or SRT/file settings.
            </div>
          )}

          <div className="channel-settings-form">
            <label className="presets-field">
              <span>Name</span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={busy || active}
                placeholder={`Decode ${client.id}`}
              />
            </label>

            <label className="presets-field">
              <span>Output format</span>
              <div className="channel-settings-actions" style={{ marginTop: 0, marginBottom: 4 }}>
                <select
                  value={formats.some((f) => f.code === formatCode) ? formatCode : formatCode || ""}
                  onChange={(e) => setFormatCode(e.target.value)}
                  disabled={busy || active || formats.length === 0}
                  style={{ flex: 1 }}
                >
                  <option value="">{formats.length ? "Select format…" : "No formats probed"}</option>
                  {formats.map((f) => (
                    <option key={f.code} value={f.code}>
                      {f.label}
                    </option>
                  ))}
                </select>
                <button type="button" className="badge" onClick={refreshDevices} disabled={busy || active}>
                  Re-probe
                </button>
              </div>
              {formats.length === 0 && (
                <span className="channel-settings-hint">No modes probed. Try Re-probe.</span>
              )}
              {probeLog && formats.length === 0 && (
                <pre className="channel-settings-logbox" style={{ marginTop: 8, maxHeight: 120 }}>
                  {probeLog}
                </pre>
              )}
            </label>

            <label className="presets-field">
              <span>Source</span>
              <select
                value={source}
                onChange={(e) => setSource(e.target.value as "srt" | "file")}
                disabled={busy || active}
              >
                <option value="srt">SRT</option>
                <option value="file">File</option>
              </select>
            </label>

            {source === "file" ? (
              <>
                <label className="presets-field">
                  <span>Media file</span>
                  <input value={fileLabel || "No file selected"} disabled readOnly />
                  <div className="channel-settings-actions" style={{ marginTop: 8 }}>
                    <button
                      type="button"
                      className="global-rec-btn"
                      disabled={busy || active}
                      onClick={() => setLibraryOpen(true)}
                    >
                      Browse recordings…
                    </button>
                    {media.length > 0 && (
                      <select
                        value={fileId.startsWith("lib:") ? "" : fileId}
                        disabled={busy || active}
                        onChange={(e) => {
                          const id = e.target.value;
                          setFileId(id);
                          setFileLabel(displayFileLabel(id, undefined, media));
                        }}
                      >
                        <option value="">Or uploaded…</option>
                        {media.map((m) => (
                          <option key={m.id} value={m.id}>
                            {m.name}
                          </option>
                        ))}
                      </select>
                    )}
                  </div>
                </label>
                <label className="presets-field" style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                  <input
                    type="checkbox"
                    checked={loop}
                    onChange={(e) => setLoop(e.target.checked)}
                    disabled={busy || active}
                  />
                  <span>Loop</span>
                </label>
              </>
            ) : (
              <>
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
              </>
            )}
          </div>

          <div className="channel-settings-actions">
            <button
              type="button"
              className={`stream-btn ${active ? "streaming" : "idle"}`}
              onClick={toggleRun}
              disabled={busy}
            >
              {busy ? "…" : active ? "STOP" : source === "file" ? "PLAY" : "START"}
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

      <LibraryModal
        open={libraryOpen}
        onClose={() => setLibraryOpen(false)}
        pickMode
        onPick={onPickRecording}
      />
    </>
  );
}
