# roc-recording

Live-preview för Blackmagic IP-kort (ST 2110) via HLS Low-Latency.

## Arkitektur

- **Backend** – Go + FFmpeg, kör på capture-hosten
- **Frontend** – Next.js + hls.js, kör separat och pekar mot backend

## Snabbstart

### Backend (capture-host, Linux)

```bash
# Med Docker
PUBLIC_URL=http://<capture-host-ip>:8080 API_KEY=hemlig docker compose up -d

# Eller direkt
cd backend
go build -o roc-recording ./cmd/server
./roc-recording config.yaml
```

Justera `backend/config.yaml` för rätt FFmpeg-input per kanal.

### Frontend

```bash
cd frontend
# Sätt backend-URL och API-nyckel i .env.local
echo "NEXT_PUBLIC_BACKEND_URL=http://<capture-host-ip>:8080" >> .env.local
echo "NEXT_PUBLIC_API_KEY=hemlig" >> .env.local
npm run dev
```

## API

Alla `/api/`-endpoints kräver header `X-API-Key: <nyckel>`.

| Metod | Path | Beskrivning |
|---|---|---|
| GET | `/api/streams` | Lista alla 8 kanaler |
| POST | `/api/streams/{id}/start` | Starta kanal |
| POST | `/api/streams/{id}/stop` | Stoppa kanal |
| GET | `/hls/{id}/index.m3u8` | HLS-playlist |

## FFmpeg-input

`ffmpeg_input` i `config.yaml` är en fri sträng med FFmpeg-inputargument per kanal.
Justera efter exakt enhetsnamn och SDK-version för ditt Blackmagic IP-kort.
