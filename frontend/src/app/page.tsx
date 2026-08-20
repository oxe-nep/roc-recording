"use client";

import { useState } from "react";
import Link from "next/link";
import StreamGrid from "@/components/StreamGrid";
import SystemStatus from "@/components/SystemStatus";
import EncodePresetsEditor from "@/components/EncodePresetsEditor";

export default function Home() {
  const [presetsOpen, setPresetsOpen] = useState(false);

  return (
    <>
      <header className="compact-header">
        <div className="header-brand">
          <img src="/nep-logo.svg" alt="NEP" className="nep-logo" />
          <Link href="/recordings" className="header-library-link">
            Recordings
          </Link>
          <button
            type="button"
            className="header-library-link header-link-btn"
            onClick={() => setPresetsOpen(true)}
          >
            Encode presets
          </button>
        </div>
        <SystemStatus />
      </header>
      <StreamGrid />
      <EncodePresetsEditor
        open={presetsOpen}
        onClose={() => setPresetsOpen(false)}
        onChanged={() => window.dispatchEvent(new Event("roc-presets-changed"))}
      />
    </>
  );
}
