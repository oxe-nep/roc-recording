"use client";

import { useEffect, useState } from "react";

const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

interface ThumbnailProps {
  id: number;
  className?: string;
}

export default function Thumbnail({ id, className }: ThumbnailProps) {
  const [src, setSrc] = useState(`${BASE}/thumb/${id}?t=${Date.now()}`);
  const [hasError, setHasError] = useState(false);

  useEffect(() => {
    const interval = setInterval(() => {
      setHasError(false);
      setSrc(`${BASE}/thumb/${id}?t=${Date.now()}`);
    }, 1000);
    return () => clearInterval(interval);
  }, [id]);

  if (hasError) {
    return (
      <div className="w-full h-full flex items-center justify-center text-xs font-mono text-slate-600">
        no signal
      </div>
    );
  }

  return (
    <img
      src={src}
      alt={`Channel ${id} preview`}
      className={className}
      onError={() => setHasError(true)}
    />
  );
}
