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
  fetchTcLoop,
  updateTcLoop,
  type LibraryFile,
  type PlayoutClient,
  type PlayoutDevice,
  type PlayoutMediaItem,
  type TcLoopPosition,
  type TcLoopSource,
} from "@/lib/api";
import LibraryModal from "@/components/LibraryModal";
import TcPositionPreview from "@/components/TcPositionPreview";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";
import {
  defaultTcUdpPort,
  tcSourceLabel,
  tcStatusLabel,
  tcStatusPillClass,
} from "@/lib/tcUi";

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
  const [tcEnabled, setTcEnabled] = useState(false);
  const [tcStatus, setTcStatus] = useState<"off" | "running" | "restarting" | "error">("off");
  const [tcSource, setTcSource] = useState<TcLoopSource>("tod");
  const [tcUdpPort, setTcUdpPort] = useState(0);
  const [tcFontSize, setTcFontSize] = useState(48);
  const [tcOpacity, setTcOpacity] = useState(0.9);
  const [tcPosition, setTcPosition] = useState<TcLoopPosition>("bottom_right");
  const [tcError, setTcError] = useState("");
  const [tcApplyMsg, setTcApplyMsg] = useState<string | null>(null);
  const logBoxRef = useRef<HTMLPreElement>(null);

  useBodyScrollLock(open);

  const channelId = client?.id;
  const active = client ? isPlayoutOn(client.status) : false;
  const tcOn = tcEnabled || tcStatus === "running";
  const tcEffectivePort = tcUdpPort > 0 ? tcUdpPort : defaultTcUdpPort(channelId ?? 0);
  const tcSourceText = tcSourceLabel(tcSource, tcEffectivePort, channelId ?? 0);
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
    Promise.all([fetchPlayoutDevices(), fetchPlayoutMedia(), fetchTcLoop(channelId)])
      .then(([devs, files, tc]) => {
        setDevices(devs);
        setMedia(files);
        setFileLabel((prev) => prev || displayFileLabel(client.file_id || "", client.file_name, files));
        setTcEnabled(!!tc.enabled);
        setTcStatus(tc.status);
        setTcSource(tc.source === "external" ? "external" : "tod");
        setTcUdpPort(tc.udp_port || defaultTcUdpPort(channelId));
        setTcFontSize(tc.fontsize || 48);
        setTcOpacity(tc.opacity ?? 0.9);
        setTcPosition(tc.position || "bottom_right");
        setTcError(tc.error || "");
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

  const applyTc = async (enabled: boolean) => {
    if (channelId == null) return;
    setBusy(true);
    setError(null);
    setTcApplyMsg(enabled ? "Starting TC…" : "Stopping TC…");
    try {
      const tc = await updateTcLoop(channelId, {
        enabled,
        source: tcSource,
        udp_port: tcSource === "external" ? tcEffectivePort : 0,
        fontsize: tcFontSize,
        opacity: tcOpacity,
        position: tcPosition,
      });
      setTcEnabled(!!tc.enabled);
      setTcStatus(tc.status);
      setTcSource(tc.source === "external" ? "external" : "tod");
      setTcUdpPort(tc.udp_port || defaultTcUdpPort(channelId));
      setTcFontSize(tc.fontsize || 48);
      setTcOpacity(tc.opacity ?? 0.9);
      setTcPosition(tc.position || "bottom_right");
      setTcError(tc.error || "");

      if (tc.enabled) {
        setTcApplyMsg("Waiting for TC…");
        for (let i = 0; i < 20; i++) {
          await new Promise((r) => setTimeout(r, 400));
          const latest = await fetchTcLoop(channelId);
          setTcStatus(latest.status);
          setTcError(latest.error || "");
          if (latest.status === "running") {
            setTcApplyMsg(null);
            break;
          }
          if (latest.status === "error") {
            setTcApplyMsg(null);
            setError(latest.error || "TC burn-in failed to start");
            break;
          }
        }
      } else {
        setTcApplyMsg(null);
      }
      onSaved();
    } catch (e) {
      setTcApplyMsg(null);
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggleRun = async () => {
    if (tcOn && !active) {
      setError("Disable TC Burn-in before starting decode playout");
      return;
    }
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

          {tcApplyMsg && <div className="channel-settings-note tc-apply-note">{tcApplyMsg}</div>}

          {!tcOn && active && (
            <div className="channel-settings-lock">
              Decode is active — stop it before changing format, source, or SRT/file settings.
            </div>
          )}

          {!tcOn && (
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
          )}

          <div className={`channel-settings-srt channel-settings-tc${tcOn ? " enabled" : ""}`}>
            <div className="channel-settings-srt-head">
              <h3>TC Burn-in</h3>
              <span className={tcStatusPillClass(tcStatus, tcEnabled)}>
                {tcStatusLabel(tcStatus, tcEnabled)}
              </span>
            </div>
            <div className="channel-settings-form">
              <div className="tc-settings-body">
                <div className="tc-settings-row">
                  <div className="tc-settings-fields">
                    <label className="presets-field">
                      <span>Timecode source</span>
                      <select
                        value={tcSource}
                        onChange={(e) => setTcSource(e.target.value as TcLoopSource)}
                        disabled={busy}
                      >
                        <option value="tod">Time of day (host clock)</option>
                        <option value="external">External (UDP)</option>
                      </select>
                    </label>
                    {tcSource === "external" && (
                      <label className="presets-field">
                        <span>UDP listen port</span>
                        <input
                          type="number"
                          min={1}
                          max={65535}
                          value={tcEffectivePort}
                          onChange={(e) =>
                            setTcUdpPort(Number(e.target.value) || defaultTcUdpPort(channelId ?? 0))
                          }
                          disabled={busy}
                        />
                      </label>
                    )}
                    <label className="presets-field">
                      <span>Font size</span>
                      <input
                        type="number"
                        min={12}
                        max={200}
                        value={tcFontSize}
                        onChange={(e) => setTcFontSize(Number(e.target.value) || 48)}
                        disabled={busy}
                      />
                    </label>
                    <label className="presets-field">
                      <span>Opacity ({Math.round(tcOpacity * 100)}%)</span>
                      <input
                        type="range"
                        min={0.2}
                        max={1}
                        step={0.05}
                        value={tcOpacity}
                        onChange={(e) => setTcOpacity(Number(e.target.value))}
                        disabled={busy}
                      />
                    </label>
                    <label className="presets-field">
                      <span>Position</span>
                      <select
                        value={tcPosition}
                        onChange={(e) => setTcPosition(e.target.value as TcLoopPosition)}
                        disabled={busy}
                      >
                        <option value="bottom_right">Bottom right</option>
                        <option value="bottom_left">Bottom left</option>
                        <option value="top_right">Top right</option>
                        <option value="top_left">Top left</option>
                        <option value="center">Center</option>
                      </select>
                    </label>
                  </div>
                  <TcPositionPreview
                    position={tcPosition}
                    fontsize={tcFontSize}
                    opacity={tcOpacity}
                  />
                </div>
              </div>
              {tcError && tcStatus === "error" && (
                <div className="channel-settings-lock tc-error-note">{tcError}</div>
              )}
            </div>
            <div className="channel-settings-actions">
              {tcOn ? (
                <>
                  <button
                    type="button"
                    className="global-rec-btn tc-apply-on"
                    onClick={() => applyTc(true)}
                    disabled={busy}
                  >
                    {busy ? "…" : "Update"}
                  </button>
                  <button
                    type="button"
                    className="tc-stop-btn"
                    onClick={() => applyTc(false)}
                    disabled={busy}
                  >
                    {busy ? "…" : "Stop burn-in"}
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  className="global-rec-btn tc-apply-on"
                  onClick={() => applyTc(true)}
                  disabled={busy}
                >
                  {busy ? "…" : "Start burn-in"}
                </button>
              )}
            </div>
          </div>

          {!tcOn && (
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
          )}

          {tcOn && (
            <div className="channel-settings-actions">
              <button type="button" className="badge" onClick={onClose} disabled={busy}>
                Close
              </button>
            </div>
          )}

          {!tcOn && (
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
          )}
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
