import { action, KeyDownEvent, SingletonAction, WillAppearEvent } from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

export type VolumeSettings = {
  target?: "pgm" | "intercom";
  slot?: number;
  direction?: "up" | "down";
};

@action({ UUID: "com.nep.commentator.volume" })
export class VolumeAction extends SingletonAction<VolumeSettings> {
  override onWillAppear(ev: WillAppearEvent<VolumeSettings>): void | Promise<void> {
    bridge.registerVolumeKey(ev.action, ev.payload.settings ?? {});
  }

  override onKeyDown(ev: KeyDownEvent<VolumeSettings>): void | Promise<void> {
    bridge.adjustVolume(ev.action.id);
  }
}
