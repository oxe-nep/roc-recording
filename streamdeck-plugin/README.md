# NEP Commentator — Stream Deck plugin

PTT and volume control for [roc-recording](../README.md) remote commentator sessions. PTT works without browser focus: the plugin connects directly to the backend controls WebSocket after pairing from the commentator web page. Volume buttons route through the browser (audio elements live there).

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

Or download the pre-built `.streamDeckPlugin` from the commentator web UI.

The bundled **NEP Commentator** profile installs automatically when you install the plugin (accept the prompt). Profiles exist for standard (15-key), Mini, and XL devices.

If buttons were added manually, open each action's property inspector and set the intercom slot / volume target.

## Layout

**Page 1 — PTT**

- Top row: intercom PTT keys (slots 0–5, left to right)
- Bottom row: PGM volume −/+, page switch to volumes

**Page 2 — Volumes**

- Per-intercom volume −/+ for each slot (0–5)
- Page switch back to PTT

Button labels and volume percentages update when paired with the commentator web session.

## Usage

1. Open the commentator invite URL and enter the PIN.
2. When connected, the web page pairs with the plugin automatically (`ws://127.0.0.1:17200`).
3. The NEP Commentator profile switches in; intercom labels update from the session.
4. Hold a PTT key — release to return to on-air mic.
5. Tap volume keys to adjust PGM or intercom levels (5% per tap).

The browser tab does **not** need focus for PTT. Audio/video still runs in the browser; volume changes are applied there.

## Protocol

**Web → plugin** (`ws://127.0.0.1:17200`):

- `{ "type": "pair", "origin", "token", "pin", "controls_path" }`
- `{ "type": "layout", "buttons": [{ "slot", "channel", "label" }] }`
- `{ "type": "volumes", "pgm": 0.8, "intercom": { "1": 0.7 } }`
- `{ "type": "unpair" }`

**Plugin → web**:

- `{ "type": "volume", "target": "pgm" | "intercom", "slot"?: N, "delta": 0.05 }`

**Plugin → backend** (`wss://…/ws/commentator/{token}/controls?pin=…`):

- `{ "type": "ptt", "channel": N }` (0 = off)

## Development

```bash
npm run watch
```

Restart the plugin from Stream Deck after code changes.

To refresh the downloadable plugin in the frontend:

```bash
npm run pack:web
```
