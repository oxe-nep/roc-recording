"use client";

import { useEffect } from "react";

let lockCount = 0;
let savedHtmlOverflow = "";
let savedBodyOverflow = "";
let savedBodyPaddingRight = "";

function applyLock() {
  const html = document.documentElement;
  const body = document.body;
  savedHtmlOverflow = html.style.overflow;
  savedBodyOverflow = body.style.overflow;
  savedBodyPaddingRight = body.style.paddingRight;
  const scrollbar = Math.max(0, window.innerWidth - html.clientWidth);
  html.style.overflow = "hidden";
  body.style.overflow = "hidden";
  if (scrollbar > 0) {
    body.style.paddingRight = `${scrollbar}px`;
  }
}

function releaseLock() {
  const html = document.documentElement;
  const body = document.body;
  html.style.overflow = savedHtmlOverflow;
  body.style.overflow = savedBodyOverflow;
  body.style.paddingRight = savedBodyPaddingRight;
}

/** Lock page scroll while a modal is open without shifting layout (scrollbar compensation). */
export function useBodyScrollLock(locked: boolean) {
  useEffect(() => {
    if (!locked) return;
    if (lockCount === 0) {
      applyLock();
    }
    lockCount += 1;
    return () => {
      lockCount = Math.max(0, lockCount - 1);
      if (lockCount === 0) {
        releaseLock();
      }
    };
  }, [locked]);
}
