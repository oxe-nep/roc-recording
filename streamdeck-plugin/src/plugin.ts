import streamDeck from "@elgato/streamdeck";
import { PTTAction } from "./actions/ptt.js";
import { VolumeAction } from "./actions/volume.js";
import { bridge } from "./bridge/bridge.js";
import { scheduleProfileActivation } from "./profile.js";

streamDeck.actions.registerAction(new PTTAction());
streamDeck.actions.registerAction(new VolumeAction());

streamDeck.devices.onDeviceDidConnect((ev) => {
  scheduleProfileActivation(ev.device.id);
});

bridge.startLocalServer();

void streamDeck.connect().then(() => {
  scheduleProfileActivation();
});
