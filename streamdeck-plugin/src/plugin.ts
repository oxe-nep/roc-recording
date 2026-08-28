import streamDeck from "@elgato/streamdeck";
import { ConnectAction } from "./actions/connect.js";
import { HostaAction } from "./actions/hosta.js";
import { PTTAction } from "./actions/ptt.js";
import { VolumeAction } from "./actions/volume.js";
import { scheduleProfileActivation } from "./profile.js";

console.log("[nep-commentator] plugin starting");

streamDeck.actions.registerAction(new PTTAction());
streamDeck.actions.registerAction(new VolumeAction());
streamDeck.actions.registerAction(new ConnectAction());
streamDeck.actions.registerAction(new HostaAction());

streamDeck.devices.onDeviceDidConnect((ev) => {
  scheduleProfileActivation(ev.device.id);
});

void streamDeck.connect().then(() => {
  console.log("[nep-commentator] plugin connected to Stream Deck");
  scheduleProfileActivation();
}).catch((err) => {
  console.error("[nep-commentator] plugin connect failed:", err);
});
