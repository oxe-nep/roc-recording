import {
  action,
  DidReceiveSettingsEvent,
  KeyDownEvent,
  SendToPluginEvent,
  SingletonAction,
  WillAppearEvent,
} from "@elgato/streamdeck";
import { bridge } from "../bridge/bridge.js";

export type ConnectSettings = {
  server?: string;
  code?: string;
};

type ConnectPluginMessage = {
  action?: "pair";
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
    void this.runPair(ev.action, ev.payload.settings ?? {});
  }

  override onSendToPlugin(ev: SendToPluginEvent<ConnectPluginMessage, ConnectSettings>): void | Promise<void> {
    if (ev.payload?.action !== "pair") return;
    void this.runPair(ev.action, {
      server: ev.payload.server,
      code: ev.payload.code,
    });
  }

  private async runPair(
    action: KeyDownEvent<ConnectSettings>["action"],
    settings: ConnectSettings,
  ): Promise<void> {
    try {
      bridge.saveConnectSettings(settings);
      const code = settings.code?.trim();
      if (!code) {
        await action.setTitle("No code");
        return;
      }
      await action.setTitle("…");
      const ok = await bridge.claimAndConnect();
      await action.setTitle(ok ? "Linked" : "Failed");
    } catch (err) {
      console.error("[nep-commentator] connect action error:", err);
      await action.setTitle("Error");
    }
  }
}
