"use client";

import { useState } from "react";
import {
  createCommentatorSession,
  revokeCommentatorSession,
  type CommentatorInfo,
} from "@/lib/api";
import { showCommentatorCard } from "@/lib/workflow";
import { sortByChannelId } from "@/lib/sortChannels";
import { useWorkflows } from "@/hooks/useWorkflows";
import { useDashboard } from "@/hooks/useDashboard";
import CommentatorSettingsModal from "@/components/CommentatorSettingsModal";

function statusLabel(info?: CommentatorInfo): string {
  if (!info || !info.enabled) return "Off";
  if (info.connected) return "Connected";
  if (info.session_active) return "Waiting for commentator";
  return "Ready";
}

function statusClass(info?: CommentatorInfo): string {
  if (!info || !info.enabled) return "status-stopped";
  if (info.connected) return "status-running";
  if (info.session_active) return "status-waiting";
  return "status-waiting";
}

export default function CommentatorGrid() {
  const { loading, streams, commentatorById } = useDashboard();
  const { workflows } = useWorkflows();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);

  const visible = sortByChannelId(streams.filter((s) => showCommentatorCard(workflows, s.id)));

  const run = async (id: number, fn: () => Promise<void>) => {
    setBusy((b) => ({ ...b, [id]: true }));
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy((b) => ({ ...b, [id]: false }));
    }
  };

  if (!loading && visible.length === 0) {
    return null;
  }

  return (
    <section className="io-section io-section-commentator">
      <div className="io-section-head">
        <h2 className="io-section-title">Remote Commentator</h2>
      </div>

      {error && (
        <div className="error-message">
          {error}
          <button type="button" className="error-dismiss" onClick={() => setError(null)} aria-label="Dismiss">
            ×
          </button>
        </div>
      )}

      {loading && visible.length === 0 ? (
        <div className="loading">
          <span>…</span>
        </div>
      ) : (
        <div className="cards-grid">
          {visible.map((s) => {
            const info = commentatorById[s.id];
            const isBusy = !!busy[s.id];
            return (
              <article key={s.id} className="channel-card commentator-card">
                <div className="card-head">
                  <span className="input-badge">{s.id}</span>
                  <span className="card-title">{s.name}</span>
                  <span className={`status-pill ${statusClass(info)}`}>{statusLabel(info)}</span>
                </div>

                <div className="card-body commentator-card-body">
                  {info?.connected ? (
                    <p className="commentator-invite-hint commentator-invite-hint--live">
                      Commentator connected
                    </p>
                  ) : info?.invite_url ? (
                    <p className="commentator-invite-hint">
                      Invite active — open Settings to copy or open the link.
                    </p>
                  ) : (
                    <p className="commentator-invite-hint">
                      Configure intercom and create an invite link.
                    </p>
                  )}
                </div>

                <div className="card-actions">
                  <button
                    type="button"
                    className="badge"
                    disabled={isBusy}
                    onClick={() => setSettingsId(s.id)}
                  >
                    Settings
                  </button>
                  {!info?.session_active ? (
                    <button
                      type="button"
                      className="badge start-btn"
                      disabled={isBusy || !info?.enabled}
                      onClick={() =>
                        void run(s.id, async () => {
                          await createCommentatorSession(s.id);
                        })
                      }
                    >
                      Create invite
                    </button>
                  ) : (
                    <button
                      type="button"
                      className="badge stop-btn"
                      disabled={isBusy}
                      onClick={() =>
                        void run(s.id, async () => {
                          await revokeCommentatorSession(s.id);
                        })
                      }
                    >
                      Revoke invite
                    </button>
                  )}
                </div>
              </article>
            );
          })}
        </div>
      )}

      <CommentatorSettingsModal
        open={settingsId != null}
        channelId={settingsId}
        onClose={() => setSettingsId(null)}
        onSaved={() => {}}
      />
    </section>
  );
}
