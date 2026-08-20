# roc-recording

Live preview and recording UI for Blackmagic DeckLink IP (ST 2110).

## Architecture

- **Backend** – Go + FFmpeg, runs on the capture host (DeckLink access)
- **Frontend** – Next.js in k3s; nginx proxies API/media to the capture host

Per running channel, one FFmpeg holds the DeckLink lock and fans out:

1. JPEG thumbnail (preview grid)
2. **Master encode** → local MPEG-TS UDP (named preset from `encode_presets:`)
3. HLS audio-only (browser listen) + peak meters

Recording starts a second FFmpeg that **remuxes** the UDP feed (`-c copy`) into fragmented MP4 — no second NVENC pass.

Optional **SRT** output per channel remuxes the same UDP master (`-c copy`) as listener or caller. Configure and start from channel settings. Publish URL host comes from `PUBLIC_SRT_HOST` (or the host in `PUBLIC_URL`). Default listener ports are `9100 + channel id`. FFmpeg must be built with libsrt.

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
