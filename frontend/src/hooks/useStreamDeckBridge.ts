"use client";

import { useEffect, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import type { CommentatorSession } from "@/lib/commentatorWebRTC";
import {
  clampVolume,
  intercomToLayout,
  type StreamDeckVolumeAdjust,
} from "@/lib/streamDeckBridge";

type Args = {
  enabled: boolean;
  token: string;
  pin: string;
  sessionRef: React.RefObject<CommentatorSession | null>;
  intercom: CommentatorIntercomSlot[];
  pgmVol: number;
  intercomVol: Record<number, number>;
  onVolumeAdjust: (adjust: StreamDeckVolumeAdjust) => void;
  onHostaChange?: (active: boolean) => void;
  onPTTChange?: (channel: number) => void;
};

export function useStreamDeckBridge({
  enabled,
  token,
  pin,
  sessionRef,
  intercom,
  pgmVol,
  intercomVol,
  onVolumeAdjust,
  onHostaChange,
  onPTTChange,
}: Args) {
  const onVolumeAdjustRef = useRef(onVolumeAdjust);
  const onHostaChangeRef = useRef(onHostaChange);
  const onPTTChangeRef = useRef(onPTTChange);
  const [pluginConnected, setPluginConnected] = useState(false);

  onVolumeAdjustRef.current = onVolumeAdjust;
  onHostaChangeRef.current = onHostaChange;
  onPTTChangeRef.current = onPTTChange;

  useEffect(() => {
    const session = sessionRef.current;
    if (!session) return;
    session.onDeckVolume = (adjust) => onVolumeAdjustRef.current(adjust);
    session.onDeckHosta = (active) => onHostaChangeRef.current?.(active);
    session.onDeckPTT = (channel) => onPTTChangeRef.current?.(channel);
    return () => {
      if (sessionRef.current === session) {
        session.onDeckVolume = undefined;
        session.onDeckHosta = undefined;
        session.onDeckPTT = undefined;
      }
    };
  }, [sessionRef, enabled]);

  useEffect(() => {
    if (!enabled || !token || !pin) {
      setPluginConnected(false);
      return;
    }
    let cancelled = false;
    const origin = typeof window !== "undefined" ? window.location.origin : "";
    const poll = async () => {
      try {
        const res = await fetch(
          `${origin}/api/commentator/join/${encodeURIComponent(token)}/deck-status?pin=${encodeURIComponent(pin)}`,
        );
        if (!res.ok) return;
        const data = (await res.json()) as { plugin_connected?: boolean };
        if (!cancelled) setPluginConnected(!!data.plugin_connected);
      } catch {
        /* plugin status is best-effort */
      }
    };
    void poll();
    const timer = setInterval(poll, 3000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [enabled, token, pin]);

  useEffect(() => {
    if (!enabled) return;
    const session = sessionRef.current;
    if (!session?.isSignalingOpen()) return;
    session.sendDeckLayout(intercomToLayout(intercom));
  }, [enabled, sessionRef, intercom, pluginConnected]);

  useEffect(() => {
    if (!enabled) return;
    const session = sessionRef.current;
    if (!session?.isSignalingOpen()) return;
    session.sendDeckVolumes(pgmVol, intercomVol);
  }, [enabled, sessionRef, pgmVol, intercomVol, pluginConnected]);

  return { pluginConnected };
}

export { clampVolume };
