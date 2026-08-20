"use client";

import { useEffect, useState } from "react";
import StreamGrid from "@/components/StreamGrid";
import SystemStatus from "@/components/SystemStatus";
import EncodePresetsEditor from "@/components/EncodePresetsEditor";
import LibraryModal from "@/components/LibraryModal";

export default function Home() {
  const [presetsOpen, setPresetsOpen] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [anyRecording, setAnyRecording] = useState(false);

  useEffect(() => {
    const openLibrary = () => setLibraryOpen(true);
    const onRecState = (e: Event) => {
      const detail = (e as CustomEvent<{ anyRecording?: boolean }>).detail;
      setAnyRecording(!!detail?.anyRecording);
    };
    window.addEventListener("roc-open-library", openLibrary);
    window.addEventListener("roc-recording-state", onRecState);
    return () => {
      window.removeEventListener("roc-open-library", openLibrary);
      window.removeEventListener("roc-recording-state", onRecState);
    };
  }, []);

  return (
    <>
      <header className="compact-header">
        <div className="header-brand">
          <img src="/nep-logo.svg" alt="NEP" className="nep-logo" />
          <button
            type="button"
            className="header-library-link header-link-btn"
            onClick={() => setLibraryOpen(true)}
          >
            Recordings
          </button>
          <button
            type="button"
            className={`header-library-link header-link-btn ${anyRecording ? "is-disabled" : ""}`}
            onClick={() => {
              if (anyRecording) return;
              setPresetsOpen(true);
            }}
            disabled={anyRecording}
            title={anyRecording ? "Locked while recording" : "Edit encode presets"}
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
      <LibraryModal
        open={libraryOpen}
        onClose={() => {
          setLibraryOpen(false);
          window.dispatchEvent(new Event("roc-library-changed"));
        }}
      />
    </>
  );
}
