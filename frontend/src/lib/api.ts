const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

// Local: set NEXT_PUBLIC_* in .env.local and talk to backend directly.
// k3s: image is built with empty NEXT_PUBLIC_* so the browser uses same-origin;
// nginx proxies to the capture host and injects X-API-Key from Secret API_KEY.

export interface Stream {
  id: number;
  name: string;
  status: "running" | "waiting" | "stopped" | "error";
  error?: string;
  format?: string;
  encode_preset: string;
  hls_url: string;
}

export function isCaptureOn(status: Stream["status"]): boolean {
  return status === "running" || status === "waiting";
}

export interface EncodePreset {
  id: string;
  label: string;
  video_codec: string;
  video_bitrate: string;
  video_maxrate: string;
  video_bufsize: string;
  video_preset: string;
  video_gop: number;
  audio_bitrate: string;
}

export interface EncodeCodecOption {
  id: string;
  label: string;
  presets: { id: string; label: string }[];
}

async function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_KEY,
      ...(init?.headers ?? {}),
    },
  });
  return res;
}

export async function fetchStreams(): Promise<Stream[]> {
  const res = await apiFetch("/api/streams");
  if (!res.ok) throw new Error(`fetchStreams: ${res.status}`);
  return res.json();
}

export async function fetchEncodePresets(): Promise<EncodePreset[]> {
  const res = await apiFetch("/api/encode/presets");
  if (!res.ok) throw new Error(`fetchEncodePresets: ${res.status}`);
  return res.json();
}

export async function fetchEncodeOptions(): Promise<EncodeCodecOption[]> {
  const res = await apiFetch("/api/encode/options");
  if (!res.ok) throw new Error(`fetchEncodeOptions: ${res.status}`);
  const body = await res.json();
  return (body.codecs ?? []) as EncodeCodecOption[];
}

export async function setEncodePreset(id: number, preset: string): Promise<Stream> {
  const res = await apiFetch(`/api/streams/${id}/encode-preset`, {
    method: "PUT",
    body: JSON.stringify({ preset }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `setEncodePreset: ${res.status}`);
  }
  return res.json();
}

export async function createEncodePreset(preset: EncodePreset): Promise<EncodePreset> {
  const res = await apiFetch("/api/encode/presets", {
    method: "POST",
    body: JSON.stringify(preset),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `createEncodePreset: ${res.status}`);
  }
  return res.json();
}

export async function updateEncodePreset(id: string, preset: Omit<EncodePreset, "id">): Promise<EncodePreset> {
  const res = await apiFetch(`/api/encode/presets/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(preset),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `updateEncodePreset: ${res.status}`);
  }
  return res.json();
}

export async function deleteEncodePreset(id: string): Promise<void> {
  const res = await apiFetch(`/api/encode/presets/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `deleteEncodePreset: ${res.status}`);
  }
}


export async function startStream(id: number): Promise<void> {
  const res = await apiFetch(`/api/streams/${id}/start`, { method: "POST" });
  if (!res.ok) throw new Error(`startStream: ${res.status}`);
}

export async function stopStream(id: number): Promise<void> {
  const res = await apiFetch(`/api/streams/${id}/stop`, { method: "POST" });
  if (!res.ok) throw new Error(`stopStream: ${res.status}`);
}

export async function fetchStreamLogs(id: number): Promise<string[]> {
  const res = await apiFetch(`/api/streams/${id}/logs`);
  if (!res.ok) throw new Error(`fetchStreamLogs: ${res.status}`);
  const body = await res.json();
  return (body.lines ?? []) as string[];
}

export interface RecordingInfo {
  id: number;
  status: "idle" | "recording";
  name: string;
  category: string;
  started_at?: string;
  file_path?: string;
  elapsed_sec?: number;
  bitrate_kbps?: number;
  encoding?: boolean;
}

export interface LibraryCategory {
  name: string;
  file_count: number;
}

export interface LibraryFile {
  category: string;
  name: string;
  size: number;
  mod_time: string;
  url: string;
}

export interface AudioLevels {
  l: number;
  r: number;
}

export interface SystemMetrics {
  cpu_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  mem_percent: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  disk_percent: number;
  disk_path?: string;
  gpu_available: boolean;
  nvenc_percent?: number;
  nvdec_percent?: number;
  gpu_percent?: number;
  gpu_mem_used_mb?: number;
  gpu_mem_total_mb?: number;
}

export async function fetchSystemMetrics(): Promise<SystemMetrics> {
  const res = await apiFetch("/api/system");
  if (!res.ok) throw new Error(`fetchSystemMetrics: ${res.status}`);
  return res.json();
}

export async function setRecordingName(id: number, name: string): Promise<RecordingInfo> {
  const res = await apiFetch(`/api/recordings/${id}/name`, {
    method: "PUT",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error(`setRecordingName: ${res.status}`);
  return res.json();
}

export async function setRecordingCategory(id: number, category: string): Promise<RecordingInfo> {
  const res = await apiFetch(`/api/recordings/${id}/category`, {
    method: "PUT",
    body: JSON.stringify({ category }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `setRecordingCategory: ${res.status}`);
  }
  return res.json();
}

export async function fetchRecordings(): Promise<RecordingInfo[]> {
  const res = await apiFetch("/api/recordings");
  if (!res.ok) throw new Error(`fetchRecordings: ${res.status}`);
  return res.json();
}

export async function startRecording(id: number): Promise<RecordingInfo> {
  const res = await apiFetch(`/api/recordings/${id}/start`, { method: "POST" });
  if (!res.ok) throw new Error(`startRecording: ${res.status}`);
  return res.json();
}

export async function stopRecording(id: number): Promise<RecordingInfo> {
  const res = await apiFetch(`/api/recordings/${id}/stop`, { method: "POST" });
  if (!res.ok) throw new Error(`stopRecording: ${res.status}`);
  return res.json();
}

export async function fetchRecordingsPath(): Promise<string> {
  const res = await apiFetch("/api/settings/recordings-path");
  if (!res.ok) throw new Error(`fetchRecordingsPath: ${res.status}`);
  const body = await res.json();
  return body.path as string;
}

export async function setRecordingsPath(path: string): Promise<string> {
  const res = await apiFetch("/api/settings/recordings-path", {
    method: "PUT",
    body: JSON.stringify({ path }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `setRecordingsPath: ${res.status}`);
  }
  const body = await res.json();
  return body.path as string;
}

export async function fetchLibraryCategories(): Promise<LibraryCategory[]> {
  const res = await apiFetch("/api/library/categories");
  if (!res.ok) throw new Error(`fetchLibraryCategories: ${res.status}`);
  return res.json();
}

export async function createLibraryCategory(name: string): Promise<LibraryCategory> {
  const res = await apiFetch("/api/library/categories", {
    method: "POST",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `createLibraryCategory: ${res.status}`);
  }
  return res.json();
}

export async function renameLibraryCategory(name: string, newName: string): Promise<LibraryCategory> {
  const res = await apiFetch(`/api/library/categories/${encodeURIComponent(name)}`, {
    method: "PUT",
    body: JSON.stringify({ name: newName }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `renameLibraryCategory: ${res.status}`);
  }
  return res.json();
}

export async function deleteLibraryCategory(name: string): Promise<void> {
  const res = await apiFetch(`/api/library/categories/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `deleteLibraryCategory: ${res.status}`);
  }
}

export async function fetchLibraryFiles(category?: string): Promise<LibraryFile[]> {
  const q = category ? `?category=${encodeURIComponent(category)}` : "";
  const res = await apiFetch(`/api/library/files${q}`);
  if (!res.ok) throw new Error(`fetchLibraryFiles: ${res.status}`);
  return res.json();
}

/** Direct media URL for <video>/<a download>. Supports Range; uses api_key query when needed locally. */
export function libraryFileURL(category: string, name: string, opts?: { download?: boolean }): string {
  const path = `/api/library/file/${encodeURIComponent(category)}/${encodeURIComponent(name)}`;
  const params = new URLSearchParams();
  if (API_KEY) params.set("api_key", API_KEY);
  if (opts?.download) params.set("download", "1");
  const q = params.toString();
  return `${BASE}${path}${q ? `?${q}` : ""}`;
}

export async function deleteLibraryFile(category: string, name: string): Promise<void> {
  const res = await apiFetch(
    `/api/library/file/${encodeURIComponent(category)}/${encodeURIComponent(name)}`,
    { method: "DELETE" },
  );
  if (!res.ok) throw new Error(`deleteLibraryFile: ${res.status}`);
}

export async function moveLibraryFile(
  fromCategory: string,
  toCategory: string,
  name: string,
): Promise<LibraryFile> {
  const res = await apiFetch("/api/library/move", {
    method: "POST",
    body: JSON.stringify({
      from_category: fromCategory,
      to_category: toCategory,
      name,
    }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `moveLibraryFile: ${res.status}`);
  }
  return res.json();
}

export async function fetchAudioLevels(id: number): Promise<AudioLevels> {
  const res = await fetch(`${BASE}/audio/${id}`);
  if (!res.ok) throw new Error(`fetchAudioLevels: ${res.status}`);
  return res.json();
}

export interface SrtInfo {
  id: number;
  status: "idle" | "streaming";
  mode: "listener" | "caller";
  port: number;
  target: string;
  has_passphrase: boolean;
  latency_ms: number;
  publish_url: string;
  error?: string;
  bitrate_kbps?: number;
  sending?: boolean;
}

export type SrtUpdateInput = {
  mode?: "listener" | "caller";
  port?: number;
  target?: string;
  passphrase?: string;
  latency_ms?: number;
};

export async function fetchSrtAll(): Promise<SrtInfo[]> {
  const res = await apiFetch("/api/srt");
  if (!res.ok) throw new Error(`fetchSrtAll: ${res.status}`);
  return res.json();
}

export async function fetchSrt(id: number): Promise<SrtInfo> {
  const res = await apiFetch(`/api/srt/${id}`);
  if (!res.ok) throw new Error(`fetchSrt: ${res.status}`);
  return res.json();
}

export async function updateSrt(id: number, body: SrtUpdateInput): Promise<SrtInfo> {
  const res = await apiFetch(`/api/srt/${id}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}));
    throw new Error(errBody.error || `updateSrt: ${res.status}`);
  }
  return res.json();
}

export async function startSrt(id: number): Promise<SrtInfo> {
  const res = await apiFetch(`/api/srt/${id}/start`, { method: "POST" });
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}));
    throw new Error(errBody.error || `startSrt: ${res.status}`);
  }
  return res.json();
}

export async function stopSrt(id: number): Promise<SrtInfo> {
  const res = await apiFetch(`/api/srt/${id}/stop`, { method: "POST" });
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}));
    throw new Error(errBody.error || `stopSrt: ${res.status}`);
  }
  return res.json();
}

export interface PlayoutFormat {
  code: string;
  label: string;
  width: number;
  height: number;
  fps: number;
  interlaced: boolean;
}

export interface PlayoutDevice {
  name: string; // unique sink id for FFmpeg
  label?: string; // display name
  open_name?: string; // name that accepted format probe
  formats: PlayoutFormat[];
  probe_log?: string;
  busy?: boolean;
  busy_reason?: string;
}

export interface PlayoutClient {
  id: number;
  name: string;
  status: "stopped" | "waiting" | "running";
  device: string;
  device_label?: string;
  format_code: string;
  decklink_out?: boolean;
  fixed?: boolean;
  source?: "srt" | "file";
  file_id?: string;
  file_name?: string;
  loop?: boolean;
  mode: "listener" | "caller";
  port: number;
  target: string;
  has_passphrase: boolean;
  latency_ms: number;
  bitrate_kbps?: number;
  sending?: boolean;
  reconnects?: number;
  error?: string;
  listen_url?: string;
}

export type PlayoutCreateInput = {
  name?: string;
  device?: string;
  device_label?: string;
  format_code?: string;
  decklink_out?: boolean;
  source?: "srt" | "file";
  file_id?: string;
  loop?: boolean;
  mode?: "listener" | "caller";
  port?: number;
  target?: string;
  passphrase?: string;
  latency_ms?: number;
};

export type PlayoutUpdateInput = {
  name?: string;
  device?: string;
  device_label?: string;
  format_code?: string;
  decklink_out?: boolean;
  source?: "srt" | "file";
  file_id?: string;
  loop?: boolean;
  mode?: "listener" | "caller";
  port?: number;
  target?: string;
  passphrase?: string;
  latency_ms?: number;
};

export interface PlayoutMediaItem {
  id: string;
  name: string;
  size: number;
  created_at: string;
}

export function isPlayoutOn(status: PlayoutClient["status"]): boolean {
  return status === "running" || status === "waiting";
}

export async function fetchPlayoutDevices(refresh = false): Promise<PlayoutDevice[]> {
  const q = refresh ? "?refresh=1" : "";
  const res = await apiFetch(`/api/playout/devices${q}`);
  if (!res.ok) throw new Error(`fetchPlayoutDevices: ${res.status}`);
  return res.json();
}

export async function fetchPlayoutClients(): Promise<PlayoutClient[]> {
  const res = await apiFetch("/api/playout");
  if (!res.ok) throw new Error(`fetchPlayoutClients: ${res.status}`);
  return res.json();
}

export async function createPlayoutClient(body: PlayoutCreateInput = {}): Promise<PlayoutClient> {
  const res = await apiFetch("/api/playout", {
    method: "POST",
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `createPlayoutClient: ${res.status}`);
  }
  return res.json();
}

export async function updatePlayoutClient(id: number, body: PlayoutUpdateInput): Promise<PlayoutClient> {
  const res = await apiFetch(`/api/playout/${id}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `updatePlayoutClient: ${res.status}`);
  }
  return res.json();
}

export async function deletePlayoutClient(id: number): Promise<void> {
  const res = await apiFetch(`/api/playout/${id}`, { method: "DELETE" });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `deletePlayoutClient: ${res.status}`);
  }
}

export async function startPlayout(id: number): Promise<PlayoutClient> {
  const res = await apiFetch(`/api/playout/${id}/start`, { method: "POST" });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `startPlayout: ${res.status}`);
  }
  return res.json();
}

export async function stopPlayout(id: number): Promise<PlayoutClient> {
  const res = await apiFetch(`/api/playout/${id}/stop`, { method: "POST" });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `stopPlayout: ${res.status}`);
  }
  return res.json();
}

export async function fetchPlayoutLogs(id: number): Promise<string[]> {
  const res = await apiFetch(`/api/playout/${id}/logs`);
  if (!res.ok) throw new Error(`fetchPlayoutLogs: ${res.status}`);
  const body = await res.json();
  return (body.lines ?? []) as string[];
}

export async function fetchPlayoutAudioLevels(id: number): Promise<AudioLevels> {
  // Public path under /audio/ (nginx already proxies it) — same pattern as encode meters.
  const res = await fetch(`${BASE}/audio/playout/${id}`);
  if (!res.ok) throw new Error(`fetchPlayoutAudioLevels: ${res.status}`);
  return res.json();
}

export async function fetchPlayoutMedia(): Promise<PlayoutMediaItem[]> {
  const res = await apiFetch("/api/playout/media");
  if (!res.ok) throw new Error(`fetchPlayoutMedia: ${res.status}`);
  return res.json();
}

export async function uploadPlayoutMedia(file: File): Promise<PlayoutMediaItem> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(`${BASE}/api/playout/media`, {
    method: "POST",
    headers: { "X-API-Key": API_KEY },
    body: form,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    if (res.status === 413) {
      throw new Error("File too large for proxy (nginx body limit). Redeploy frontend with 16g limit.");
    }
    if (res.status === 401) {
      throw new Error("Unauthorized — check API key / nginx inject");
    }
    if (res.status === 404) {
      throw new Error("Upload endpoint missing — rebuild/restart backend on capture host");
    }
    throw new Error(err.error || `uploadPlayoutMedia: ${res.status}`);
  }
  return res.json();
}

export async function deletePlayoutMedia(id: string): Promise<void> {
  const res = await apiFetch(`/api/playout/media/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `deletePlayoutMedia: ${res.status}`);
  }
}


