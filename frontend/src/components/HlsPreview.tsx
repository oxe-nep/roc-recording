"use client";

import { useEffect, useRef } from "react";
import Hls from "hls.js";
import { mediaURL } from "@/lib/mediaBase";

type Props = {
  active: boolean;
  /** Selected stereo pair 0–3, or null when muted. */
  listenPair: number | null;
  /** Absolute path on backend, e.g. /hls/playout/1/preview.m3u8 */
  playlistPath: string;
  /** Remount/reload key when pipeline restarts (e.g. TC stop→start). */
  sessionKey?: string | number;
};

function listenPlaylist(previewPath: string, pair: number): string {
  return previewPath.replace(/preview\.m3u8$/, `listen_${pair}.m3u8`);
}

function attachHls(
  el: HTMLMediaElement,
  src: string,
  onFatal: () => void,
): Hls | null {
  if (Hls.isSupported()) {
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: true,
      liveSyncDurationCount: 2,
      liveMaxLatencyDurationCount: 4,
      maxLiveSyncPlaybackRate: 1.5,
    });
    hls.loadSource(src);
    hls.attachMedia(el);
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      void el.play().catch(() => {});
    });
    hls.on(Hls.Events.ERROR, (_ev, data) => {
      if (!data.fatal) return;
      hls.destroy();
      onFatal();
    });
    return hls;
  }
  if (el.canPlayType("application/vnd.apple.mpegurl")) {
    el.src = src;
    void el.play().catch(() => {});
  }
  return null;
}

function stopMedia(el: HTMLMediaElement | null, hls: Hls | null) {
  hls?.destroy();
  if (!el) return;
  el.pause();
  el.removeAttribute("src");
  el.load();
}

/** Low-latency HLS video preview; listen audio is a separate stereo playlist per pair. */
export default function HlsPreview({ active, listenPair, playlistPath, sessionKey }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const audioRef = useRef<HTMLAudioElement>(null);
  const videoHls = useRef<Hls | null>(null);
  const audioHls = useRef<Hls | null>(null);

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
      stopMedia(video, videoHls.current);
      videoHls.current = null;
    };

    if (!active) {
      stop();
      return;
    }

    const src = mediaURL(playlistPath);

    const attach = () => {
      if (cancelled || !videoRef.current) return;
      stop();
      videoHls.current = attachHls(videoRef.current, src, () => {
        if (!cancelled) retryTimer = setTimeout(attach, 1000);
      });
    };

    attach();

    return () => {
      cancelled = true;
      stop();
    };
  }, [active, playlistPath, sessionKey]);

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;

    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;

    const stop = () => {
      if (retryTimer != null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      stopMedia(audio, audioHls.current);
      audioHls.current = null;
    };

    if (!active || listenPair == null) {
      stop();
      return;
    }

    const src = mediaURL(listenPlaylist(playlistPath, listenPair));

    const attach = () => {
      if (cancelled || !audioRef.current) return;
      stop();
      audioHls.current = attachHls(audioRef.current, src, () => {
        if (!cancelled) retryTimer = setTimeout(attach, 1000);
      });
    };

    attach();

    return () => {
      cancelled = true;
      stop();
    };
  }, [active, listenPair, playlistPath, sessionKey]);

  return (
    <>
      {!active && <span className="no-signal" aria-hidden />}
      <video
        ref={videoRef}
        className={`hls-preview${active ? "" : " hls-preview-off"}`}
        playsInline
        muted
        autoPlay
      />
      <audio ref={audioRef} className="audio-monitor" preload="none" />
    </>
  );
}
