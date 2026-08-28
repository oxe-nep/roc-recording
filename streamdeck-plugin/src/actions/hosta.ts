import { action, KeyDownEvent, KeyUpEvent, SingletonAction } from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

@action({ UUID: "com.nep.commentator.hosta" })
export class HostaAction extends SingletonAction {
  override onKeyDown(ev: KeyDownEvent): void | Promise<void> {
    bridge.hostaDown();
    void ev.action.setState(1);
  }

  override onKeyUp(ev: KeyUpEvent): void | Promise<void> {
    bridge.hostaUp();
    void ev.action.setState(0);
  }

  override onWillDisappear(): void | Promise<void> {
    bridge.hostaUp();
  }
}
