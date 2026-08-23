"use client";

import { useEffect, useRef } from "react";
import Hls from "hls.js";
import { mediaURL } from "@/lib/mediaBase";

type Props = {
  active: boolean;
  listening: boolean;
  /** Absolute path on backend, e.g. /hls/playout/1/preview.m3u8 */
  playlistPath: string;
  /** Remount/reload key when pipeline restarts (e.g. TC stop→start). */
  sessionKey?: string | number;
};

/** Low-latency HLS video+audio preview for dashboard cards. */
export default function HlsPreview({ active, listening, playlistPath, sessionKey }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;

    const stop = () => {
      if (retryTimer != null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      hlsRef.current?.destroy();
      hlsRef.current = null;
      video.pause();
      video.removeAttribute("src");
      video.load();
    };

    if (!active) {
      stop();
      return;
    }

    const src = mediaURL(playlistPath);

    const attach = () => {
      if (cancelled) return;
      stop();
      if (!videoRef.current) return;
      const el = videoRef.current;

      if (Hls.isSupported()) {
        const hls = new Hls({
          enableWorker: true,
          lowLatencyMode: true,
          liveSyncDurationCount: 2,
          liveMaxLatencyDurationCount: 4,
          maxLiveSyncPlaybackRate: 1.5,
        });
        hlsRef.current = hls;
        hls.loadSource(src);
        hls.attachMedia(el);
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
          el.muted = !listening;
          void el.play().catch(() => {});
        });
        hls.on(Hls.Events.ERROR, (_ev, data) => {
          if (!data.fatal || cancelled) return;
          // Playlist often appears a second after TC/ffmpeg starts — retry.
          hls.destroy();
          hlsRef.current = null;
          retryTimer = setTimeout(attach, 1000);
        });
      } else if (el.canPlayType("application/vnd.apple.mpegurl")) {
        el.src = src;
        el.muted = !listening;
        void el.play().catch(() => {});
      }
    };

    attach();

    return () => {
      cancelled = true;
      stop();
    };
    // listening is applied in a separate effect; omit to avoid full remount on mute toggle
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, playlistPath, sessionKey]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !listening;
    if (listening && active) {
      void video.play().catch(() => {});
    }
  }, [listening, active]);

  return (
    <>
      {!active && <span className="no-signal">No signal</span>}
      <video
        ref={videoRef}
        className={`hls-preview${active ? "" : " hls-preview-off"}`}
        playsInline
        muted={!listening}
        autoPlay
      />
    </>
  );
}
