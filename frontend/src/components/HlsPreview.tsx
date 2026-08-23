"use client";

import { useEffect, useRef } from "react";
import Hls from "hls.js";
import { mediaURL } from "@/lib/mediaBase";

type Props = {
  active: boolean;
  listening: boolean;
  /** Absolute path on backend, e.g. /hls/playout/1/preview.m3u8 */
  playlistPath: string;
};

/** Low-latency HLS video+audio preview for TC cards. */
export default function HlsPreview({ active, listening, playlistPath }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    const stop = () => {
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
      hls.attachMedia(video);
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        video.muted = !listening;
        void video.play().catch(() => {});
      });
    } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = src;
      video.muted = !listening;
      void video.play().catch(() => {});
    }

    return stop;
  }, [active, playlistPath]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !listening;
    if (listening && active) {
      void video.play().catch(() => {});
    }
  }, [listening, active]);

  if (!active) {
    return <span className="no-signal">No signal</span>;
  }

  return (
    <video
      ref={videoRef}
      className="hls-preview"
      playsInline
      muted={!listening}
      autoPlay
    />
  );
}
