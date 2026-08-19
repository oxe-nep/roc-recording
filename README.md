# roc-recording

Live preview for Blackmagic IP card (ST 2110) via HLS Low-Latency.

## Architecture

- **Backend** – Go + FFmpeg, runs on the capture host
- **Frontend** – Next.js + hls.js, runs separately and points to the backend

## Quick start

### Backend (capture host, Linux)

```bash
# With Docker
PUBLIC_URL=http://10.199.28.249:8080 API_KEY=secret docker compose up -d

# Or directly
cd backend
go build -o roc-recording ./cmd/server
./roc-recording config.yaml
```

Adjust `backend/config.yaml` for the correct FFmpeg input per channel.

### Frontend

```bash
cd frontend
# Set backend URL and API key in .env.local
echo "NEXT_PUBLIC_BACKEND_URL=http://10.199.28.249:8080" >> .env.local
echo "NEXT_PUBLIC_API_KEY=secret" >> .env.local
npm run dev
```

## API

All `/api/` endpoints require the header `X-API-Key: <key>`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/streams` | List all 8 channels |
| POST | `/api/streams/{id}/start` | Start channel |
| POST | `/api/streams/{id}/stop` | Stop channel |
| GET | `/hls/{id}/index.m3u8` | HLS playlist |

## FFmpeg input

`ffmpeg_input` in `config.yaml` is a free-form FFmpeg input argument string per channel.
Adjust to match the exact device name and SDK version of your Blackmagic IP card.
