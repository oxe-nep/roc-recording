"use client";

import { useEffect, useRef, useState } from "react";
import {
  createCommentatorSession,
  fetchCommentator,
  fetchCommentatorSettings,
  revokeCommentatorSession,
  updateCommentatorSettings,
  type CommentatorIntercomSlot,
} from "@/lib/api";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";
import { absoluteInviteURL } from "@/lib/commentatorWebRTC";

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

export default function CommentatorSettingsModal({ open, channelId, onClose, onSaved }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [intercom, setIntercom] = useState<CommentatorIntercomSlot[]>(defaultSlots());
  const [inviteURL, setInviteURL] = useState("");
  const [sessionActive, setSessionActive] = useState(false);
  const [status, setStatus] = useState<string>("off");
  const hydratedRef = useRef(false);

  useBodyScrollLock(open);

  useEffect(() => {
    if (!open || channelId == null) {
      hydratedRef.current = false;
      return;
    }
    if (hydratedRef.current) return;
    hydratedRef.current = true;
    setError(null);
    Promise.all([fetchCommentatorSettings(channelId), fetchCommentator(channelId)])
      .then(([settings, info]) => {
        setIntercom(settings.intercom?.length ? settings.intercom : defaultSlots());
        setInviteURL(info.invite_url ?? "");
        setSessionActive(!!info.session_active);
        setStatus(info.status);
      })
      .catch((e) => setError(String(e)));
  }, [open, channelId]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, busy]);

  if (!open || channelId == null) return null;

  const saveSettings = async () => {
    setBusy(true);
    setError(null);
    try {
      const saved = await updateCommentatorSettings(channelId, { intercom });
      setIntercom(saved.intercom);
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
          <h2>Remote Commentator — Channel {channelId}</h2>
          <button type="button" className="modal-close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}

        <div className="channel-settings-section commentator-settings-section">
          <h3>Status</h3>
          <p className="commentator-status-line">
            <span className={`status-pill ${sessionActive ? "status-waiting" : "status-stopped"}`}>
              {status}
            </span>
          </p>
        </div>

        <div className="channel-settings-section commentator-settings-section">
          <h3>Intercom channels</h3>
          <p className="commentator-settings-hint">
            Enable and name up to six mono intercom channels (DeckLink tracks 3–8). PGM uses tracks 1–2.
          </p>
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
                  disabled={busy}
                  onChange={(e) => {
                    const next = [...intercom];
                    next[idx] = { ...slot, name: e.target.value };
                    setIntercom(next);
                  }}
                />
              </div>
            ))}
          </div>
          <button type="button" className="badge start-btn" disabled={busy} onClick={() => void saveSettings()}>
            Save intercom
          </button>
        </div>

        <div className="channel-settings-section commentator-settings-section">
          <h3>Invite link</h3>
          {displayInvite ? (
            <div className="commentator-invite-row">
              <input className="commentator-invite-url" readOnly value={displayInvite} />
              <button type="button" className="badge" disabled={busy} onClick={() => void copyInvite()}>
                Copy
              </button>
              <button type="button" className="badge" disabled={busy} onClick={openInvite}>
                Open
              </button>
            </div>
          ) : (
            <p className="commentator-settings-hint">No active invite. Create a link for the commentator.</p>
          )}
          <div className="commentator-invite-actions">
            {!sessionActive ? (
              <button type="button" className="badge start-btn" disabled={busy} onClick={() => void createInvite()}>
                Create invite
              </button>
            ) : (
              <button type="button" className="badge stop-btn" disabled={busy} onClick={() => void revokeInvite()}>
                Revoke invite
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
