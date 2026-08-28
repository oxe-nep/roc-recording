import streamDeck from "@elgato/streamdeck";
import { PTTAction } from "./actions/ptt.js";
import { bridge } from "./bridge/bridge.js";

streamDeck.actions.registerAction(new PTTAction());
bridge.startLocalServer();
streamDeck.connect();
