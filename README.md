# roc-recording

Live preview and recording UI for Blackmagic DeckLink IP (ST 2110).

## Architecture

- **Backend** – Go + FFmpeg, runs on the capture host (DeckLink access)
- **Frontend** – Next.js in k3s; nginx proxies API/media to the capture host

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
| GET | `/thumb/{id}` | JPEG thumbnail |
| GET | `/audio/{id}` | Audio levels |
| GET | `/hls/{id}/audio.m3u8` | Audio monitor HLS |
