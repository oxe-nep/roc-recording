"use client";

import { useEffect, useRef } from "react";
import type Hls from "hls.js";
import { mediaURL } from "@/lib/mediaBase";
import { attachHls, stopMedia } from "@/lib/hlsPlayer";

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
  const q = previewPath.indexOf("?");
  const path = q >= 0 ? previewPath.slice(0, q) : previewPath;
  const query = q >= 0 ? previewPath.slice(q) : "";
  return path.replace(/preview\.m3u8$/, `listen_${pair}.m3u8`) + query;
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
