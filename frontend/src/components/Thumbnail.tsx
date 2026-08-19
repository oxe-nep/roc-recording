"use client";

import { useEffect, useState } from "react";

const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

interface ThumbnailProps {
  id: number;
  active: boolean;
}

export default function Thumbnail({ id, active }: ThumbnailProps) {
  const [src, setSrc] = useState(`${BASE}/thumb/${id}?t=${Date.now()}`);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    if (!active) {
      setHasError(true);
      return;
    }
    setHasError(false);
    const interval = setInterval(() => {
      setHasError(false);
      setSrc(`${BASE}/thumb/${id}?t=${Date.now()}`);
    }, 1000);
    return () => clearInterval(interval);
  }, [id, active]);

  if (!active || hasError) {
    return <span className="no-signal">No signal</span>;
  }

  return (
    <img
      src={src}
      alt={`Channel ${id}`}
      onError={() => setHasError(true)}
    />
  );
}
