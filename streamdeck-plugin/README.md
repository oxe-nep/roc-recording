# NEP Commentator — Stream Deck plugin

PTT control for [roc-recording](../README.md) remote commentator sessions. Works without browser focus: the plugin connects directly to the backend controls WebSocket after pairing from the commentator web page.

## One-time setup

1. Install [Stream Deck](https://www.elgato.com/stream-deck) (6.6+).
2. Install Node.js 20+.
3. Build and install the plugin:

```bash
cd streamdeck-plugin
npm install
npm run build
npx streamdeck link com.nep.commentator.sdPlugin
```

4. In Stream Deck, drag **Intercom PTT** actions to the top row (left to right). Slot 0 = first key, slot 1 = second, etc. Up to 6 intercom channels are mapped in order.

## Usage

1. Open the commentator invite URL and enter the PIN.
2. When connected, the web page pairs with the plugin automatically (`ws://127.0.0.1:17200`).
3. Intercom button labels on the Stream Deck update from the web session.
4. Hold a key for PTT — release to return to on-air mic.

The browser tab does **not** need focus. Audio/video still runs in the browser.

## Protocol

**Web → plugin** (`ws://127.0.0.1:17200`):

- `{ "type": "pair", "origin", "token", "pin", "controls_path" }`
- `{ "type": "layout", "buttons": [{ "slot", "channel", "label" }] }`
- `{ "type": "unpair" }`

**Plugin → backend** (`wss://…/ws/commentator/{token}/controls?pin=…`):

- `{ "type": "ptt", "channel": N }` (0 = off)

## Development

```bash
npm run watch
```

Restart the plugin from Stream Deck after code changes.
