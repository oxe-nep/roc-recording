"use client";

type Props = {
  token: string;
};

export default function CommentatorClient({ token }: Props) {
  return (
    <div className="commentator-shell">
      <header className="commentator-header">
        <h1>Remote Commentator</h1>
        <p className="commentator-subtitle">WebRTC session (coming in next phase)</p>
      </header>
      <section className="commentator-placeholder">
        <p>Session token: {token.slice(0, 8)}…</p>
        <p>Video, audio faders, and PTT controls will appear here once WebRTC is wired up.</p>
      </section>
    </div>
  );
}
