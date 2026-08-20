import type { Metadata } from "next";
import Link from "next/link";
import StreamGrid from "@/components/StreamGrid";
import SystemStatus from "@/components/SystemStatus";

export const metadata: Metadata = {
  title: "roc-recording",
};

export default function Home() {
  return (
    <>
      <header className="compact-header">
        <div className="header-brand">
          <img src="/nep-logo.svg" alt="NEP" className="nep-logo" />
          <Link href="/recordings" className="header-library-link">
            Recordings
          </Link>
        </div>
        <SystemStatus />
      </header>
      <StreamGrid />
    </>
  );
}
