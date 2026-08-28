# NEP Commentator — Stream Deck plugin

PTT and volume control for [roc-recording](../README.md) remote commentator sessions.

## How it works

The plugin connects to the commentator backend over **WSS** (no localhost bridge). The commentator web page relays layout and volume state through the server.

1. **PTT** — Stream Deck → backend (`/ws/commentator/{token}/controls`)
2. **Volume** — Stream Deck → backend → commentator web page (audio runs in the browser)
3. **Labels** — commentator web page → backend → Stream Deck

## Setup

1. Install [Stream Deck](https://www.elgato.com/stream-deck) (6.6+).
2. Download and install the plugin from the commentator web UI (or `npm run pack:web`).
3. Accept the bundled **NEP Commentator** profile when prompted.
4. On the commentator page: connect with PIN and copy the **pairing code**.
5. On Stream Deck: tap **Connect**, enter server URL + pairing code in the property inspector, press Connect again.

## Profile layout

- **Page 1:** intercom PTT, PGM volume, Connect, page switch
- **Page 2:** per-intercom volume −/+

Profiles are generated for standard (15-key), Mini, and XL devices.

## Development

```bash
npm install
npm run watch          # rebuild plugin on changes
npm run pack:web       # pack + copy to frontend/public/downloads
```

Restart the plugin from Stream Deck after code changes.
