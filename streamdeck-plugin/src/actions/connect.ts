import { action, DidReceiveSettingsEvent, KeyDownEvent, SingletonAction, WillAppearEvent } from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

export type ConnectSettings = {
  server?: string;
  code?: string;
};

@action({ UUID: "com.nep.commentator.connect" })
export class ConnectAction extends SingletonAction<ConnectSettings> {
  override onWillAppear(ev: WillAppearEvent<ConnectSettings>): void | Promise<void> {
    bridge.saveConnectSettings(ev.payload.settings ?? {});
    void ev.action.setTitle("Connect");
  }

  override onDidReceiveSettings(ev: DidReceiveSettingsEvent<ConnectSettings>): void | Promise<void> {
    bridge.saveConnectSettings(ev.payload.settings ?? {});
  }

  override onKeyDown(ev: KeyDownEvent<ConnectSettings>): void | Promise<void> {
    const settings = ev.payload.settings ?? {};
    bridge.saveConnectSettings(settings);
    const code = settings.code?.trim();
    if (!code) {
      void ev.action.setTitle("No code");
      return;
    }
    void ev.action.setTitle("…");
    void bridge.claimAndConnect().then((ok) => {
      void ev.action.setTitle(ok ? "Linked" : "Failed");
    });
  }
}
