"use client";

import { useEffect, useRef } from "react";
import Hls from "hls.js";

const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

interface AudioMonitorProps {
  id: number;
  active: boolean;
  listening: boolean;
  /** Default /hls/{id}/audio.m3u8; decode uses /hls/playout/{id}/audio.m3u8 */
  playlistPath?: string;
}

export default function AudioMonitor({ id, active, listening, playlistPath }: AudioMonitorProps) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const hlsRef = useRef<Hls | null>(null);

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;

    const stop = () => {
      hlsRef.current?.destroy();
      hlsRef.current = null;
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    };

    if (!listening || !active) {
      stop();
      return;
    }

    const src = `${BASE}${playlistPath ?? `/hls/${id}/audio.m3u8`}`;

    if (Hls.isSupported()) {
      const hls = new Hls({ enableWorker: true, lowLatencyMode: true });
      hlsRef.current = hls;
      hls.loadSource(src);
      hls.attachMedia(audio);
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        void audio.play().catch(() => {});
      });
    } else if (audio.canPlayType("application/vnd.apple.mpegurl")) {
      audio.src = src;
      void audio.play().catch(() => {});
    }

    return stop;
  }, [id, active, listening, playlistPath]);

  return <audio ref={audioRef} className="audio-monitor" preload="none" />;
}
