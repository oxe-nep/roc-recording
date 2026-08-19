"use client";

import { useEffect, useState } from "react";

const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

interface ThumbnailProps {
  id: number;
  className?: string;
}

export default function Thumbnail({ id, className }: ThumbnailProps) {
  const [src, setSrc] = useState(`${BASE}/thumb/${id}?t=${Date.now()}`);

  useEffect(() => {
    const interval = setInterval(() => {
      setSrc(`${BASE}/thumb/${id}?t=${Date.now()}`);
    }, 2000);
    return () => clearInterval(interval);
  }, [id]);

  return (
    <img
      src={src}
      alt={`Channel ${id} preview`}
      className={className}
      onError={(e) => {
        (e.target as HTMLImageElement).style.display = "none";
      }}
    />
  );
}
