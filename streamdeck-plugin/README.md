# NEP Commentator — Stream Deck plugin

PTT and volume control for [roc-recording](../README.md) remote commentator sessions.

## How it works

The plugin connects to the commentator backend over **WSS** (no localhost bridge). The commentator web page relays layout and volume state through the server.

1. **PTT** — Stream Deck → backend (`/ws/commentator/{token}/controls`)
2. **Volume** — Stream Deck → backend → commentator web page (audio runs in the browser)
3. **Labels** — commentator web page → backend → Stream Deck

## Setup

1. Install [Stream Deck](https://www.elgato.com/stream-deck) (6.9+).
2. Download and install the plugin from the commentator web UI (or `npm run pack:web`).
3. Accept the bundled **NEP Commentator** profile when prompted.
4. On the commentator page: connect with PIN and copy the **pairing code**.
5. On Stream Deck: tap **Connect**, enter server URL + pairing code in the property inspector, press **Pair now** or tap the Connect key again.

## Profile layout

**Page 1 — Intercom:** PTT keys + PGM volume + Connect + page switch  
**Page 2 — Volume:** paired −/+ per intercom + back to page 1

| Device | Page 1 | Page 2 |
|--------|--------|--------|
| **XL** | 6× PTT + PGM ± (top row), Connect + Hosta below | 6× intercom vol pairs |
| **Standard** | 5× PTT, 6th + PGM ±, Connect + Hosta | 6× intercom vol pairs |
| **Mini** | 5× PTT + Connect | PGM ±, Hosta, 1× intercom vol |

Built with `@elgato/streamdeck` 2.x and manifest `SDKVersion` 3 (Marketplace-compatible).

## Development

```bash
npm install
npm run link           # build + symlink into Stream Deck Plugins folder
npm run watch          # rebuild on changes (run link once first)
npm run pack:web       # pack + copy to frontend/public/downloads
```

Restart the plugin from **Stream Deck → Plugins → NEP Commentator** after code changes.

### Logs

Plugin logs (after a successful start):

- **Windows:** `%APPDATA%\Elgato\StreamDeck\Plugins\com.nep.commentator.sdPlugin\logs\`
- **macOS:** `~/Library/Application Support/com.elgato.StreamDeck/Plugins/com.nep.commentator.sdPlugin/logs/`

Tail on Windows:

```powershell
Get-Content "$env:APPDATA\Elgato\StreamDeck\Plugins\com.nep.commentator.sdPlugin\logs\com.nep.commentator.0.log" -Wait -Tail 50
```

If **no logs folder exists**, the Node process never started. Common causes:

- Missing `node_modules/@elgato/streamdeck` or `node_modules/ws` inside the `*.sdPlugin` folder (the pack step runs `npm install` there).
- `ws` bundled into `bin/plugin.js` (must stay external — bundling breaks with "Dynamic require of events is not supported").
- Broken ESM load of `bin/plugin.js` (requires `bin/package.json` with `"type":"module"`).

Stream Deck app logs: `%APPDATA%\Elgato\StreamDeck\logs\StreamDeck*.log` — look for `Process stopped (unexpected): code=0x00000001`.

### Debugging

`manifest.json` has `"Nodejs": { "Debug": "enabled" }`. After the plugin starts you should see a Node target in `chrome://inspect/#devices` or via **Attach to Node Process** in VS Code/Cursor.

Fixed debug port (optional):

```json
"Debug": "--inspect=127.0.0.1:9230"
```

Property inspector (Connect panel): enable `html_remote_debugging_enabled=1` in registry, restart Stream Deck, open `http://localhost:23654/` in Chrome.
