"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function RecordingsRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/");
    // Open library after landing on the grid.
    const t = window.setTimeout(() => {
      window.dispatchEvent(new Event("roc-open-library"));
    }, 50);
    return () => window.clearTimeout(t);
  }, [router]);
  return null;
}
