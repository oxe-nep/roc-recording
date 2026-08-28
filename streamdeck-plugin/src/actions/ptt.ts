import {
  action,
  KeyDownEvent,
  KeyUpEvent,
  SingletonAction,
  WillAppearEvent,
} from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

@action({ UUID: "com.nep.commentator.ptt" })
export class PTTAction extends SingletonAction {
  override onWillAppear(ev: WillAppearEvent): void | Promise<void> {
    bridge.registerKey(ev.action, ev.payload.coordinates.row, ev.payload.coordinates.column);
    void bridge.applyLayoutToAction(ev.action);
  }

  override onKeyDown(ev: KeyDownEvent): void | Promise<void> {
    bridge.pttDown(ev.action.id);
    void ev.action.setState(1);
  }

  override onKeyUp(ev: KeyUpEvent): void | Promise<void> {
    bridge.pttUp();
    void ev.action.setState(0);
  }

  override onWillDisappear(): void | Promise<void> {
    bridge.pttUp();
  }
}
