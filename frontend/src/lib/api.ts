const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

export interface Stream {
  id: number;
  name: string;
  status: "running" | "stopped" | "error";
  error?: string;
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
