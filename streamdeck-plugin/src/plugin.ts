import streamDeck from "@elgato/streamdeck";
import { ConnectAction } from "./actions/connect.js";
import { PTTAction } from "./actions/ptt.js";
import { VolumeAction } from "./actions/volume.js";
import { scheduleProfileActivation } from "./profile.js";

streamDeck.actions.registerAction(new PTTAction());
streamDeck.actions.registerAction(new VolumeAction());
streamDeck.actions.registerAction(new ConnectAction());

streamDeck.devices.onDeviceDidConnect((ev) => {
  scheduleProfileActivation(ev.device.id);
});

void streamDeck.connect().then(() => {
  scheduleProfileActivation();
});
