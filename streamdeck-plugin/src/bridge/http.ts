import http from "node:http";
import https from "node:https";

export async function postJson<T>(
  urlString: string,
  body: unknown,
): Promise<{ ok: boolean; status: number; data: T | null; text: string }> {
  const url = new URL(urlString);
  const payload = JSON.stringify(body);
  const transport = url.protocol === "https:" ? https : http;

  return new Promise((resolve, reject) => {
    const req = transport.request(
      url,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(payload),
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (chunk: Buffer) => chunks.push(chunk));
        res.on("end", () => {
          const text = Buffer.concat(chunks).toString("utf8");
          let data: T | null = null;
          if (text) {
            try {
              data = JSON.parse(text) as T;
            } catch {
              data = null;
            }
          }
          const status = res.statusCode ?? 0;
          resolve({ ok: status >= 200 && status < 300, status, data, text });
        });
      },
    );
    req.on("error", reject);
    req.write(payload);
    req.end();
  });
}
