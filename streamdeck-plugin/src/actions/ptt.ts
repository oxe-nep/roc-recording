import { action, KeyDownEvent, KeyUpEvent, SingletonAction, WillAppearEvent } from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

type PTTSettings = {
  slot?: number;
};

@action({ UUID: "com.nep.commentator.ptt" })
export class PTTAction extends SingletonAction<PTTSettings> {
  override onWillAppear(ev: WillAppearEvent<PTTSettings>): void | Promise<void> {
    const { row, column } = ev.payload.coordinates;
    const slot = ev.payload.settings?.slot ?? column + row * 5;
    bridge.registerKey(ev.action, slot);
  }

  override onKeyDown(ev: KeyDownEvent<PTTSettings>): void | Promise<void> {
    bridge.pttDown(ev.action.id);
    void ev.action.setState(1);
  }

  override onKeyUp(ev: KeyUpEvent<PTTSettings>): void | Promise<void> {
    bridge.pttUp();
    void ev.action.setState(0);
  }

  override onWillDisappear(): void | Promise<void> {
    bridge.pttUp();
  }
}
