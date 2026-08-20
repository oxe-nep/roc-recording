"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function LegacyChannelRecordingsRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/");
    const t = window.setTimeout(() => {
      window.dispatchEvent(new Event("roc-open-library"));
    }, 50);
    return () => window.clearTimeout(t);
  }, [router]);
  return null;
}
