import streamDeck from "@elgato/streamdeck";
import { PTTAction } from "./actions/ptt.js";
import { VolumeAction } from "./actions/volume.js";
import { bridge } from "./bridge/bridge.js";

streamDeck.actions.registerAction(new PTTAction());
streamDeck.actions.registerAction(new VolumeAction());
bridge.startLocalServer();
streamDeck.connect();
