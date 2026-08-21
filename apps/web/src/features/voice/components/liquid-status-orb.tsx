"use client";

import { useEffect, useRef, useState } from "react";

import styles from "../voice.module.css";

export function LiquidStatusOrb() {
  const frameRef = useRef<HTMLIFrameElement>(null);
  const [fallback, setFallback] = useState(false);

  useEffect(() => {
    const handleMessage = (event: MessageEvent<{ type?: string }>) => {
      if (event.source !== frameRef.current?.contentWindow) return;
      if (event.data?.type === "lingow-liquid-orb:error") setFallback(true);
    };

    const checkFrameStatus = () => {
      const status = frameRef.current?.contentDocument?.getElementById("status");
      if (status && !status.hidden) setFallback(true);
    };
    const statusTimer = window.setInterval(checkFrameStatus, 250);
    const statusTimeout = window.setTimeout(() => window.clearInterval(statusTimer), 3000);

    window.addEventListener("message", handleMessage);
    return () => {
      window.removeEventListener("message", handleMessage);
      window.clearInterval(statusTimer);
      window.clearTimeout(statusTimeout);
    };
  }, []);

  return (
    <span
      className={styles.liquidStatusOrb}
      data-fallback={fallback ? "true" : undefined}
      aria-hidden="true"
    >
      <span className={styles.liquidStatusOrbFallback} />
      <iframe
        className={
          fallback
            ? `${styles.liquidStatusOrbFrame} ${styles.liquidStatusOrbFrameHidden}`
            : styles.liquidStatusOrbFrame
        }
        ref={frameRef}
        src="/media/liquid-status-orb.html"
        tabIndex={-1}
        title=""
      />
    </span>
  );
}
