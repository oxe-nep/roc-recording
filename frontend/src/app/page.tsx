import type { Metadata } from "next";
import StreamGrid from "@/components/StreamGrid";

export const metadata: Metadata = {
  title: "roc-recording – Preview",
};

export default function Home() {
  return (
    <main className="min-h-screen bg-slate-950 text-white p-6">
      <header className="mb-8">
        <h1 className="text-2xl font-bold tracking-tight">roc-recording</h1>
        <p className="text-slate-400 text-sm mt-1">Live preview – 8 channels</p>
      </header>
      <StreamGrid />
    </main>
  );
}
