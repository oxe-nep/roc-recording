const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

// Local: set NEXT_PUBLIC_* in .env.local and talk to backend directly.
// k3s: image is built with empty NEXT_PUBLIC_* so the browser uses same-origin;
// nginx proxies to the capture host and injects X-API-Key from Secret API_KEY.

export interface Stream {
  id: number;
  name: string;
  status: "running" | "stopped" | "error";
  error?: string;
  format?: string;
  hls_url: string;
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

export async function startStream(id: number): Promise<void> {
  const res = await apiFetch(`/api/streams/${id}/start`, { method: "POST" });
  if (!res.ok) throw new Error(`startStream: ${res.status}`);
}

export async function stopStream(id: number): Promise<void> {
  const res = await apiFetch(`/api/streams/${id}/stop`, { method: "POST" });
  if (!res.ok) throw new Error(`stopStream: ${res.status}`);
}

export interface RecordingInfo {
  id: number;
  status: "idle" | "recording";
  name: string;
  started_at?: string;
  file_path?: string;
  elapsed_sec?: number;
  bitrate_kbps?: number;
}

export interface SystemMetrics {
  cpu_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  mem_percent: number;
  gpu_available: boolean;
  gpu_percent?: number;
  gpu_mem_used_mb?: number;
  gpu_mem_total_mb?: number;
}

export interface RecordingFile {
  name: string;
  size: number;
  mod_time: string;
  url: string;
}

export interface AudioLevels {
  l: number;
  r: number;
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

export async function startAllRecordings(): Promise<void> {
  const res = await apiFetch("/api/recordings/start-all", { method: "POST" });
  if (!res.ok) throw new Error(`startAllRecordings: ${res.status}`);
}

export async function stopAllRecordings(): Promise<void> {
  const res = await apiFetch("/api/recordings/stop-all", { method: "POST" });
  if (!res.ok) throw new Error(`stopAllRecordings: ${res.status}`);
}

export async function fetchRecordingFiles(id: number): Promise<RecordingFile[]> {
  const res = await apiFetch(`/api/recordings/files/${id}`);
  if (!res.ok) throw new Error(`fetchRecordingFiles: ${res.status}`);
  return res.json();
}

export async function fetchRecordingBlob(id: number, name: string): Promise<Blob> {
  const res = await apiFetch(`/api/recordings/file/${id}/${encodeURIComponent(name)}`);
  if (!res.ok) throw new Error(`fetchRecordingBlob: ${res.status}`);
  return res.blob();
}

export async function deleteRecordingFile(id: number, name: string): Promise<void> {
  const res = await apiFetch(`/api/recordings/file/${id}/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!res.ok) throw new Error(`deleteRecordingFile: ${res.status}`);
}

export async function fetchAudioLevels(id: number): Promise<AudioLevels> {
  const res = await fetch(`${BASE}/audio/${id}`);
  if (!res.ok) throw new Error(`fetchAudioLevels: ${res.status}`);
  return res.json();
}
