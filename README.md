# roc-recording

Live preview and recording UI for Blackmagic DeckLink IP (ST 2110).

## Architecture

- **Backend** – Go + FFmpeg, runs on the capture host (DeckLink access)
- **Frontend** – Next.js in k3s; nginx proxies API/media to the capture host

Per running channel, one FFmpeg holds the DeckLink lock and fans out:

1. JPEG thumbnail (preview grid)
2. **Master encode** → local MPEG-TS **multicast** UDP (`239.255.28.<id>:21000+id`, so REC and SRT can both subscribe)
3. HLS audio-only (browser listen) + peak meters

Recording starts a second FFmpeg that **remuxes** the UDP feed (`-c copy`) into fragmented MP4 — no second NVENC pass.

Optional **SRT** output per channel remuxes the same UDP master (`-c copy`) as listener or caller. REC and STREAM can run at the same time. Configure and start from channel settings. Publish URL host comes from `PUBLIC_SRT_HOST` (or the host in `PUBLIC_URL`). Default listener ports are `9100 + channel id`. FFmpeg must be built with libsrt.

**Decode (playout)** clients receive SRT and output to a DeckLink device (`playout-clients.json`). Devices/formats are probed via FFmpeg. Each client has its own output mode, SRT listener/caller settings, JPEG preview, and audio meters. UI sections: **Encode** (capture) and **Decode** (playout).

**Remote commentator** (branch `feature/remote-commentator`): workflow mode that dedicates a DeckLink channel pair to a WebRTC commentator bridge. Settings → Workflows → *Remote Commentator*. Restore point before this work: git tag `pre-remote-commentator` on `main`.

- **DeckLink IN** → program video + PGM (1–2) + up to 6 intercom mono (3–8) → WebRTC to browser
- **DeckLink OUT** ← commentator webcam + mic (on air 1–2, or PTT to intercom 3–8)
- Kommentator-UI: `https://recording.nepsweden.tech/commentator/{token}` (invite link from dashboard)
- Media går **direkt** browser ↔ capture host (+ TURN); signaling proxas via k3s frontend

**FFmpeg:** din befintliga DeckLink-build räcker troligen — **ingen full ombyggnad** om `libopus` redan finns:

```bash
/usr/local/bin/ffmpeg -hide_banner -encoders 2>/dev/null | grep libopus
```

Commentator-ljud **ut** till webbläsaren kodas med `-c:a libopus`. DeckLink OUT använder `pcm_s16le` + `v210` (som TC/playout). Mic **in** från webbläsaren dekodas i Go (`libopus`), inte FFmpeg. Saknas `libopus`, bygg om FFmpeg med `--enable-libopus` (samma build-skript som för decklink/libsrt).

**WebRTC env** (`/etc/roc-recording.env`):

```
COMMENTATOR_PUBLIC_URL=https://recording.nepsweden.tech
WEBRTC_STUN_URLS=stun:stun.l.google.com:19302
WEBRTC_TURN_URLS=turn:YOUR_HOST:3478?transport=udp
WEBRTC_TURN_USERNAME=commentator
WEBRTC_TURN_CREDENTIAL=...
WEBRTC_PUBLIC_HOST=10.199.28.249
```

TURN (coturn) krävs för kommentatorer bakom NAT. Exempelconfig: [`deploy/coturn/turnserver.conf.example`](deploy/coturn/turnserver.conf.example).

**Deploy backend efter pull:**

```bash
sudo ./deploy/roc-ctl.sh build   # go mod tidy + go build på capture host
sudo ./deploy/roc-ctl.sh restart
```

Encode presets are defined in `config.yaml` (and live-edited via the UI into `encode-presets.json`). Per-channel selection is persisted to `encode-assignments.json`. Encode settings are applied when that channel’s capture starts — editing a preset or switching assignment does not restart a running channel.

Recordings land in global category folders under a configurable storage root
(default `recordings_dir` in config, overridable in the library UI → saved to
`recordings-path.json`):

```
{storage}/{category}/{recname}_{YYYY-MM-DD_HH-MM-SS}.mp4
```

Default category is `_unsorted`. Create/rename/delete categories and browse all files in the **Recordings** library UI.

## Quick start

### Backend (capture host, Linux) — recommended: systemd

Do **not** run the binary in a random SSH session. Use systemd so FFmpeg children
are killed cleanly when you stop/restart, even after reconnecting.

```bash
cd /opt/application/roc-recording
git pull
chmod +x deploy/roc-ctl.sh

# First time
sudo ./deploy/roc-ctl.sh install   # unit + /etc/roc-recording.env
sudo nano /etc/roc-recording.env   # PUBLIC_URL / API_KEY
sudo ./deploy/roc-ctl.sh build
sudo ./deploy/roc-ctl.sh start
```

Daily ops:

```bash
sudo ./deploy/roc-ctl.sh status
sudo ./deploy/roc-ctl.sh restart
sudo ./deploy/roc-ctl.sh stop
sudo ./deploy/roc-ctl.sh logs
sudo ./deploy/roc-ctl.sh cleanup   # stop + kill leftover ffmpeg/roc-recording
```

Manual one-shot (not recommended):

```bash
cd backend
go build -o roc-recording ./cmd/server
PUBLIC_URL=http://10.199.28.249:8080 API_KEY=change-me ./roc-recording config.yaml
```

### Frontend (local)

```bash
cd frontend
echo "NEXT_PUBLIC_BACKEND_URL=http://10.199.28.249:8080" >> .env.local
echo "NEXT_PUBLIC_API_KEY=change-me" >> .env.local
npm run dev
```

## k3s (frontend only)

Same pattern as `roc-wg-monitor`:

1. CI builds/pushes `ghcr.io/oxe-nep/roc-recording-frontend:latest` on push to `main`
2. Apply manifests:

```bash
kubectl apply -f k8s-deployment.yaml
```

- Hostname: `recording.nepsweden.tech`
- Secret `roc-recording-secrets`: `API_KEY`, `BACKEND_HOST`, `BACKEND_PORT`
- nginx injects `X-API-Key` on proxied `/api/` calls (key is not baked into the image)

Requires cluster prerequisites: `ghcr-creds`, Traefik `https-redirect`, `letsencrypt` certResolver.

## API

All `/api/` endpoints require the header `X-API-Key: <key>`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/streams` | List channels |
| POST | `/api/streams/{id}/start` | Start preview |
| POST | `/api/streams/{id}/stop` | Stop preview |
| GET | `/api/encode/presets` | List encode presets |
| PUT | `/api/streams/{id}/encode-preset` | Set channel preset (`{"preset":"hq"}`) |
| GET | `/thumb/{id}` | JPEG thumbnail |
| GET | `/audio/{id}` | Audio levels |
| GET | `/hls/{id}/audio.m3u8` | Audio monitor HLS |
