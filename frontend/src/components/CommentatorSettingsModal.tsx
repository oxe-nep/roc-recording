"use client";

import { useEffect, useRef, useState } from "react";
import {
  createCommentatorSession,
  fetchCommentator,
  fetchCommentatorSettings,
  fetchPlayoutDevices,
  revokeCommentatorSession,
  updateCommentatorSettings,
  type CommentatorInfo,
  type CommentatorIntercomSlot,
  type PlayoutDevice,
} from "@/lib/api";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";
import { useDashboard } from "@/hooks/useDashboard";
import { absoluteInviteURL } from "@/lib/commentatorWebRTC";
import { commentatorIsActive, commentatorStatusLabel, commentatorStatusPillClass } from "@/lib/commentatorUi";

const SLOT_COUNT = 6;

type Props = {
  open: boolean;
  channelId: number | null;
  onClose: () => void;
  onSaved: () => void;
};

function defaultSlots(): CommentatorIntercomSlot[] {
  return Array.from({ length: SLOT_COUNT }, (_, i) => ({
    id: i + 1,
    name: `Intercom ${i + 1}`,
    enabled: i < 2,
  }));
}

function infoFromState(
  enabled: boolean,
  status: CommentatorInfo["status"],
  sessionActive: boolean,
  connected: boolean,
): CommentatorInfo {
  return {
    id: 0,
    enabled,
    status,
    session_active: sessionActive,
    connected,
    ptt_channel: 0,
    intercom: [],
  };
}

function formatDisplay(code?: string): string {
  const c = code?.trim();
  if (!c) return "";
  if (c.length <= 4) return c.toUpperCase();
  return c;
}

export default function CommentatorSettingsModal({ open, channelId, onClose, onSaved }: Props) {
  const { playout } = useDashboard();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [intercom, setIntercom] = useState<CommentatorIntercomSlot[]>(defaultSlots());
  const [displayName, setDisplayName] = useState("");
  const [outputFormat, setOutputFormat] = useState("");
  const [devices, setDevices] = useState<PlayoutDevice[]>([]);
  const [inviteURL, setInviteURL] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [sessionActive, setSessionActive] = useState(false);
  const [connected, setConnected] = useState(false);
  const [status, setStatus] = useState<CommentatorInfo["status"]>("off");
  const hydratedRef = useRef(false);

  useBodyScrollLock(open);

  const playoutClient = channelId != null ? playout.find((p) => p.id === channelId) : undefined;
  const deviceName = playoutClient?.device || "";
  const deviceLabel = playoutClient?.device_label || deviceName;
  const selectedDevice = devices.find((d) => d.name === deviceName || d.open_name === deviceName);
  const formats = selectedDevice?.formats ?? [];
  const probeLog = selectedDevice?.probe_log;

  useEffect(() => {
    if (!open || channelId == null) {
      hydratedRef.current = false;
      return;
    }
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    setError(null);
    Promise.all([
      fetchCommentatorSettings(channelId),
      fetchCommentator(channelId),
      fetchPlayoutDevices(),
    ])
      .then(([settings, info, devs]) => {
        setIntercom(settings.intercom?.length ? settings.intercom : defaultSlots());
        setDisplayName(settings.display_name?.trim() ?? "");
        setOutputFormat(
          settings.output_format?.trim() ||
            info.output_format?.trim() ||
            playoutClient?.format_code?.trim() ||
            "",
        );
        setDevices(devs);
        setInviteURL(info.invite_url ?? "");
        setEnabled(!!info.enabled);
        setSessionActive(!!info.session_active);
        setConnected(!!info.connected);
        setStatus(info.status);
      })
      .catch((e) => setError(String(e)));
  }, [open, channelId, playoutClient?.format_code]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, busy]);

  if (!open || channelId == null) return null;

  const uiInfo = infoFromState(enabled, status, sessionActive, connected);
  const active = commentatorIsActive(uiInfo);
  const outputLocked = connected;

  const refreshDevices = async () => {
    setBusy(true);
    setError(null);
    try {
      const devs = await fetchPlayoutDevices(true);
      setDevices(devs);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const saveSettings = async () => {
    setBusy(true);
    setError(null);
    try {
      const saved = await updateCommentatorSettings(channelId, {
        intercom,
        display_name: displayName.trim(),
        output_format: outputFormat.trim(),
      });
      setIntercom(saved.intercom);
      setDisplayName(saved.display_name?.trim() ?? displayName.trim());
      setOutputFormat(saved.output_format?.trim() ?? outputFormat.trim());
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const createInvite = async () => {
    setBusy(true);
    setError(null);
    try {
      const session = await createCommentatorSession(channelId);
      setInviteURL(session.invite_url);
      setSessionActive(true);
      setStatus("session_active");
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const revokeInvite = async () => {
    setBusy(true);
    setError(null);
    try {
      const info = await revokeCommentatorSession(channelId);
      setInviteURL(info.invite_url ?? "");
      setSessionActive(!!info.session_active);
      setConnected(!!info.connected);
      setStatus(info.status);
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const copyInvite = async () => {
    const url = absoluteInviteURL(inviteURL);
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
    } catch (e) {
      setError(String(e));
    }
  };

  const openInvite = () => {
    const url = absoluteInviteURL(inviteURL);
    if (url) window.open(url, "_blank", "noopener,noreferrer");
  };

  const displayInvite = absoluteInviteURL(inviteURL);

  return (
    <div className="modal-backdrop" onClick={() => !busy && onClose()} role="presentation">
      <div
        className="modal-panel channel-settings-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label={`Remote commentator channel ${channelId}`}
      >
        <div className="modal-header">
          <h2>
            <span className="input-badge decode">{channelId}</span>
            <span>Commentator</span>
          </h2>
          <button type="button" className="modal-close" onClick={onClose} aria-label="Close" disabled={busy}>
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        <div className={`channel-settings-srt channel-settings-commentator${active ? " enabled" : ""}`}>
          <div className="channel-settings-srt-head">
            <h3>Session</h3>
            <span className={commentatorStatusPillClass(uiInfo)}>{commentatorStatusLabel(uiInfo)}</span>
          </div>
          <p className="channel-settings-hint">
            Remote commentator uses WebRTC for low-latency return video and audio. PGM uses DeckLink tracks 1–2;
            intercom channels use tracks 3–8. The invite link is persistent until you revoke it or the server is
            reconfigured.
          </p>

          <div className="channel-settings-form" style={{ marginBottom: 12 }}>
            <label className="presets-field">
              <span>Commentator name</span>
              <input
                value={displayName}
                disabled={busy}
                placeholder="e.g. Studio A — Match 1"
                onChange={(e) => setDisplayName(e.target.value)}
              />
            </label>
            <p className="channel-settings-hint" style={{ marginTop: 4 }}>
              Shown in the commentator browser header instead of a generic title.
            </p>
          </div>

          {displayInvite ? (
            <div className="srt-url-row">
              <code className="srt-url" title={displayInvite}>
                {displayInvite}
              </code>
              <button type="button" className="badge" disabled={busy} onClick={() => void copyInvite()}>
                Copy
              </button>
              <button type="button" className="badge" disabled={busy} onClick={openInvite}>
                Open
              </button>
            </div>
          ) : enabled ? (
            <p className="channel-settings-hint">Invite link will appear when remote commentator is active.</p>
          ) : (
            <p className="channel-settings-hint">Enable remote commentator workflow to get a persistent invite link.</p>
          )}

          <div className="channel-settings-actions">
            {!sessionActive ? (
              <button type="button" className="tc-start-btn" disabled={busy || !enabled} onClick={() => void createInvite()}>
                {busy ? "…" : "Ensure invite link"}
              </button>
            ) : (
              <button type="button" className="tc-stop-btn" disabled={busy} onClick={() => void revokeInvite()}>
                {busy ? "…" : "Revoke invite"}
              </button>
            )}
          </div>
        </div>

        <div className="channel-settings-srt">
          <div className="channel-settings-srt-head">
            <h3>DeckLink output</h3>
          </div>
          <p className="channel-settings-hint">
            Webcam return video is played out on this DeckLink output. Format applies on the next commentator
            connection{outputLocked ? " (disconnect commentator to change)" : ""}.
          </p>
          <div className="channel-settings-form">
            <label className="presets-field">
              <span>Device</span>
              <input value={deviceLabel || deviceName || "—"} readOnly disabled />
            </label>
            <label className="presets-field">
              <span>Output format</span>
              <div className="channel-settings-actions" style={{ marginTop: 0, marginBottom: 4 }}>
                <select
                  value={formats.some((f) => f.code === outputFormat) ? outputFormat : outputFormat || ""}
                  onChange={(e) => setOutputFormat(e.target.value)}
                  disabled={busy || outputLocked || formats.length === 0}
                  style={{ flex: 1 }}
                >
                  <option value="">{formats.length ? "Format…" : "—"}</option>
                  {formats.map((f) => (
                    <option key={f.code} value={f.code}>
                      {f.label || formatDisplay(f.code)}
                    </option>
                  ))}
                </select>
                <button type="button" className="badge" onClick={() => void refreshDevices()} disabled={busy || outputLocked}>
                  Re-probe
                </button>
              </div>
              {outputFormat && (
                <span className="channel-settings-hint">Selected: {formatDisplay(outputFormat)}</span>
              )}
              {formats.length === 0 && probeLog && (
                <pre className="channel-settings-logbox" style={{ marginTop: 8, maxHeight: 120 }}>
                  {probeLog}
                </pre>
              )}
            </label>
          </div>
        </div>

        <div className="channel-settings-srt">
          <div className="channel-settings-srt-head">
            <h3>Intercom channels</h3>
          </div>
          <p className="channel-settings-hint">
            Enable and name up to six mono intercom channels. Disabled channels are not sent to the commentator.
          </p>
          <div className="channel-settings-form">
            <div className="commentator-intercom-list">
              {intercom.map((slot, idx) => (
                <div key={slot.id} className="commentator-intercom-row">
                  <label className="commentator-intercom-enable">
                    <input
                      type="checkbox"
                      checked={slot.enabled}
                      disabled={busy}
                      onChange={(e) => {
                        const next = [...intercom];
                        next[idx] = { ...slot, enabled: e.target.checked };
                        setIntercom(next);
                      }}
                    />
                    <span>Ch {slot.id}</span>
                  </label>
                  <input
                    className="commentator-intercom-name"
                    value={slot.name}
                    disabled={busy || !slot.enabled}
                    placeholder={`Intercom ${slot.id}`}
                    onChange={(e) => {
                      const next = [...intercom];
                      next[idx] = { ...slot, name: e.target.value };
                      setIntercom(next);
                    }}
                  />
                </div>
              ))}
            </div>
          </div>
          <div className="channel-settings-actions">
            <button type="button" className="global-rec-btn" disabled={busy || outputLocked} onClick={() => void saveSettings()}>
              {busy ? "…" : "Save settings"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
