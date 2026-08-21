"use client";

import { useEffect, useRef } from "react";

import styles from "../voice.module.css";

const STRAND_COUNT = 23;

export function AuroraStrands() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const context = canvas.getContext("2d");
    if (!context) return;

    let displayWidth = 0;
    let displayHeight = 0;

    const resize = () => {
      const bounds = canvas.getBoundingClientRect();
      const scale = Math.min(window.devicePixelRatio || 1, 2);
      displayWidth = bounds.width;
      displayHeight = bounds.height;
      canvas.width = Math.max(1, Math.round(displayWidth * scale));
      canvas.height = Math.max(1, Math.round(displayHeight * scale));
      context.setTransform(scale, 0, 0, scale, 0, 0);
    };

    const draw = () => {
      const width = displayWidth;
      const height = displayHeight;
      if (!width || !height) return;

      context.clearRect(0, 0, width, height);
      context.lineCap = "round";
      context.lineJoin = "round";
      const amplitude = 1;

      for (let strand = 0; strand < STRAND_COUNT; strand += 1) {
        const offset = strand - (STRAND_COUNT - 1) / 2;
        const distance = Math.abs(offset) / (STRAND_COUNT / 2);
        const gradient = context.createLinearGradient(0, 0, width, 0);
        const alpha = 0.09 + (1 - distance) * 0.37;

        gradient.addColorStop(0, "rgb(255 255 255 / 0)");
        gradient.addColorStop(0.16, `rgb(255 255 255 / ${alpha * 0.58})`);
        gradient.addColorStop(0.5, `rgb(255 255 255 / ${alpha + 0.18})`);
        gradient.addColorStop(0.84, `rgb(255 255 255 / ${alpha * 0.58})`);
        gradient.addColorStop(1, "rgb(255 255 255 / 0)");

        context.beginPath();
        for (let x = 0; x <= width; x += 2) {
          const progress = x / width;
          const envelope = Math.pow(Math.sin(progress * Math.PI), 1.7);
          const primaryWave = Math.sin(
            progress * Math.PI * 4.2 + strand * 0.31,
          );
          const detailWave = Math.sin(
            progress * Math.PI * 8.5 - strand * 0.17,
          );
          const y =
            height / 2 +
            offset * 2.1 +
            envelope *
              amplitude *
              (primaryWave * height * 0.21 + detailWave * height * 0.032);

          if (x === 0) context.moveTo(x, y);
          else context.lineTo(x, y);
        }

        context.strokeStyle = gradient;
        context.lineWidth = strand % 5 === 0 ? 1.1 : 0.62;
        context.shadowBlur = 5;
        context.shadowColor = "rgb(255 255 255 / 0.2)";
        context.stroke();
      }
    };

    resize();
    draw();
    const resizeObserver = new ResizeObserver(() => {
      resize();
      draw();
    });
    resizeObserver.observe(canvas);

    return () => {
      resizeObserver.disconnect();
    };
  }, []);

  return (
    <span className={styles.strands} aria-hidden="true">
      <canvas className={styles.strandCanvas} ref={canvasRef} />
    </span>
  );
}
