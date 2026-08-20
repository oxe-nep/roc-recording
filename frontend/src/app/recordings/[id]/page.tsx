"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function LegacyChannelRecordingsRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/recordings");
  }, [router]);
  return <div className="recordings-page">Redirecting to library…</div>;
}
