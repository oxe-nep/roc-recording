import type { Metadata } from "next";
import StreamGrid from "@/components/StreamGrid";

export const metadata: Metadata = {
  title: "roc-recording",
};

export default function Home() {
  return (
    <>
      <header className="compact-header">
        <div className="header-brand">
          <img src="/nep-logo.svg" alt="NEP" className="nep-logo" />
        </div>
      </header>
      <StreamGrid />
    </>
  );
}
