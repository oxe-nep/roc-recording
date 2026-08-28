"use client";

import { useEffect, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import {
  clampVolume,
  intercomToLayout,
  StreamDeckBridge,
  type StreamDeckBridgeStatus,
  type StreamDeckVolumeAdjust,
} from "@/lib/streamDeckBridge";

type Args = {
  enabled: boolean;
  origin: string;
  token: string;
  pin: string;
  controlsPath: string;
  intercom: CommentatorIntercomSlot[];
  pgmVol: number;
  intercomVol: Record<number, number>;
  onVolumeAdjust: (adjust: StreamDeckVolumeAdjust) => void;
};

export function useStreamDeckBridge({
  enabled,
  origin,
  token,
  pin,
  controlsPath,
  intercom,
  pgmVol,
  intercomVol,
  onVolumeAdjust,
}: Args) {
  const bridgeRef = useRef<StreamDeckBridge | null>(null);
  const onVolumeAdjustRef = useRef(onVolumeAdjust);
  const [status, setStatus] = useState<StreamDeckBridgeStatus>("offline");
  const [controlsConnected, setControlsConnected] = useState(false);

  onVolumeAdjustRef.current = onVolumeAdjust;

  useEffect(() => {
    if (!enabled || !token || !pin || !controlsPath) {
      bridgeRef.current?.stop();
      bridgeRef.current = null;
      setStatus("offline");
      setControlsConnected(false);
      return;
    }

    const bridge = new StreamDeckBridge({
      onStatus: (next) => {
        setStatus(next);
        if (next === "ready" || next === "paired") {
          bridge.pair({ origin, token, pin, controlsPath });
        }
      },
      onControlsConnected: setControlsConnected,
      onVolumeAdjust: (adjust) => onVolumeAdjustRef.current(adjust),
    });
    bridgeRef.current = bridge;
    bridge.start();

    return () => {
      bridge.stop();
      bridgeRef.current = null;
    };
  }, [enabled, origin, token, pin, controlsPath]);

  useEffect(() => {
    if (!enabled || status !== "paired") return;
    bridgeRef.current?.publishLayout(intercomToLayout(intercom));
  }, [enabled, status, intercom]);

  useEffect(() => {
    if (!enabled || status !== "paired") return;
    bridgeRef.current?.publishVolumes(pgmVol, intercomVol);
  }, [enabled, status, pgmVol, intercomVol]);

  return { status, controlsConnected };
}

export { clampVolume };
