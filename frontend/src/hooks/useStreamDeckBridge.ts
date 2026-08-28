"use client";

import { useEffect, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import {
  intercomToLayout,
  StreamDeckBridge,
  type StreamDeckBridgeStatus,
} from "@/lib/streamDeckBridge";

type Args = {
  enabled: boolean;
  origin: string;
  token: string;
  pin: string;
  controlsPath: string;
  intercom: CommentatorIntercomSlot[];
};

export function useStreamDeckBridge({
  enabled,
  origin,
  token,
  pin,
  controlsPath,
  intercom,
}: Args) {
  const bridgeRef = useRef<StreamDeckBridge | null>(null);
  const [status, setStatus] = useState<StreamDeckBridgeStatus>("offline");
  const [controlsConnected, setControlsConnected] = useState(false);

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

  return { status, controlsConnected };
}
