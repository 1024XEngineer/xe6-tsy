"use client";

import styles from "../voice.module.css";

export function LiquidStatusOrb({ status = "ready" }: { status?: "ready" | "switching" | "error" }) {
  return (
    <span
      className={styles.liquidStatusOrb}
      data-status={status}
      aria-hidden="true"
    />
  );
}
