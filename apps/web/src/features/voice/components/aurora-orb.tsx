"use client";

import { useEffect, useRef } from "react";

import styles from "../voice.module.css";

const FILAMENT_COUNT = 188;
const CORE_RING_COUNT = 11;
const FRAME_INTERVAL = 1000 / 30;
const TWO_PI = Math.PI * 2;

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(Math.max(value, minimum), maximum);
}

export function AuroraOrb() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const context = canvas.getContext("2d");
    if (!context) return;

    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    let animationFrame = 0;
    let lastFrame = 0;
    let displayWidth = 0;
    let displayHeight = 0;
    let hoverTarget = 0;
    let hoverAmount = 0;
    let pointerTargetX = 0;
    let pointerTargetY = 0;
    let pointerX = 0;
    let pointerY = 0;

    const resize = () => {
      const bounds = canvas.getBoundingClientRect();
      const scale = Math.min(window.devicePixelRatio || 1, 2);
      displayWidth = bounds.width;
      displayHeight = bounds.height;
      canvas.width = Math.max(1, Math.round(displayWidth * scale));
      canvas.height = Math.max(1, Math.round(displayHeight * scale));
      context.setTransform(scale, 0, 0, scale, 0, 0);
    };

    const draw = (elapsed: number) => {
      if (!displayWidth || !displayHeight) return;

      const time = (motionQuery.matches ? 1800 : elapsed) * 0.001;
      const centerX = displayWidth / 2;
      const centerY = displayHeight / 2;
      const radius = Math.min(displayWidth, displayHeight) * 0.455;
      hoverAmount += (hoverTarget - hoverAmount) * 0.075;
      pointerX += (pointerTargetX - pointerX) * 0.065;
      pointerY += (pointerTargetY - pointerY) * 0.065;
      const pointerAngle = Math.atan2(pointerY, pointerX);
      const pointerStrength = Math.min(Math.hypot(pointerX, pointerY), 1);
      const breath = Math.sin(time * 0.76) * 0.013;
      const shapeDrift = Math.sin(time * 0.28) * 0.15;
      const lightAngle = -0.72 + time * 0.12 + Math.sin(time * 0.18) * 0.14;
      const twist =
        0.062 + Math.sin(time * 0.24) * 0.012 + hoverAmount * 0.05;

      const outerRadiusAt = (angle: number) =>
        radius *
        (0.815 +
          breath +
          Math.sin(angle * 3 - 0.62 + shapeDrift) * 0.047 +
          Math.sin(angle * 5 + 0.92 - time * 0.18) * 0.022 +
          Math.cos(angle - 0.24) * 0.012 +
          Math.sin(angle * 2 + 1.3 + time * 0.07) * 0.007 +
          Math.sin(angle * 7 - time * 0.5) * 0.005 +
          Math.max(0, Math.cos(angle - pointerAngle)) *
            hoverAmount *
            pointerStrength *
            0.034);

      const innerRadiusAt = (angle: number) =>
        radius *
        (0.39 +
          breath * 0.18 +
          Math.sin(angle * 2 - 0.35 + time * 0.055) * 0.0035);

      context.clearRect(0, 0, displayWidth, displayHeight);
      context.lineCap = "round";
      context.globalCompositeOperation = "source-over";
      context.shadowBlur = 2.6;
      context.shadowColor = "rgb(255 255 255 / 0.16)";

      for (let ring = 1; ring <= CORE_RING_COUNT; ring += 1) {
        const progress = ring / CORE_RING_COUNT;
        const baseRadius = radius * (0.035 + progress * 0.345);
        context.beginPath();

        for (let sample = 0; sample <= FILAMENT_COUNT; sample += 1) {
          const angle = (sample / FILAMENT_COUNT) * TWO_PI - Math.PI / 2;
          const contourRadius =
            baseRadius *
            (1 +
              Math.sin(angle * 3 + time * 0.22 + ring * 0.17) *
                (0.012 + progress * 0.014) +
              Math.sin(angle * 5 - time * 0.13 - ring * 0.11) * 0.008);
          const x = centerX + Math.cos(angle) * contourRadius;
          const y = centerY + Math.sin(angle) * contourRadius;
          if (sample === 0) context.moveTo(x, y);
          else context.lineTo(x, y);
        }

        context.closePath();
        context.strokeStyle = `rgb(255 255 255 / ${0.035 + progress * 0.105})`;
        context.lineWidth = 0.52;
        context.stroke();
      }

      for (let index = 0; index < FILAMENT_COUNT; index += 1) {
        const angle = (index / FILAMENT_COUNT) * TWO_PI - Math.PI / 2;
        const innerRadius = innerRadiusAt(angle);
        const outerRadius = outerRadiusAt(angle);
        const illumination = Math.pow(
          0.5 + 0.5 * Math.cos(angle - lightAngle),
          0.72,
        );
        const lengthFactor = clamp(
          (outerRadius - innerRadius) / (radius * 0.48),
          0,
          1,
        );
        const alpha =
          0.18 +
          illumination * (0.49 + hoverAmount * 0.08) +
          lengthFactor * 0.08;
        const outerAngle =
          angle + twist + Math.sin(angle * 2 + time * 0.09) * 0.005;
        const controlRadiusA = innerRadius + (outerRadius - innerRadius) * 0.34;
        const controlRadiusB = innerRadius + (outerRadius - innerRadius) * 0.72;
        const controlAngleA = angle - twist * 0.28;
        const controlAngleB = angle + twist * 0.25;

        const startX = centerX + Math.cos(angle) * innerRadius;
        const startY = centerY + Math.sin(angle) * innerRadius;
        const controlAX = centerX + Math.cos(controlAngleA) * controlRadiusA;
        const controlAY = centerY + Math.sin(controlAngleA) * controlRadiusA;
        const controlBX = centerX + Math.cos(controlAngleB) * controlRadiusB;
        const controlBY = centerY + Math.sin(controlAngleB) * controlRadiusB;
        const endX = centerX + Math.cos(outerAngle) * outerRadius;
        const endY = centerY + Math.sin(outerAngle) * outerRadius;

        context.beginPath();
        context.moveTo(startX, startY);
        context.bezierCurveTo(
          controlAX,
          controlAY,
          controlBX,
          controlBY,
          endX,
          endY,
        );
        context.strokeStyle = `rgb(255 255 255 / ${alpha})`;
        context.lineWidth = 0.58 + illumination * 0.32;
        context.stroke();
      }

      context.shadowBlur = 0;
      context.beginPath();
      for (let sample = 0; sample <= FILAMENT_COUNT; sample += 1) {
        const angle = (sample / FILAMENT_COUNT) * TWO_PI - Math.PI / 2;
        const innerRadius = innerRadiusAt(angle) - 0.35;
        const x = centerX + Math.cos(angle) * innerRadius;
        const y = centerY + Math.sin(angle) * innerRadius;
        if (sample === 0) context.moveTo(x, y);
        else context.lineTo(x, y);
      }
      context.closePath();
      context.strokeStyle = "rgb(255 255 255 / 0.2)";
      context.lineWidth = 0.55;
      context.stroke();

      context.beginPath();
      context.arc(centerX, centerY, 0.85 + Math.sin(time * 0.8) * 0.18, 0, TWO_PI);
      context.fillStyle = "rgb(255 255 255 / 0.34)";
      context.fill();
    };

    const render = (elapsed: number) => {
      if (elapsed - lastFrame >= FRAME_INTERVAL || motionQuery.matches) {
        draw(elapsed);
        lastFrame = elapsed;
      }
      if (!motionQuery.matches) animationFrame = requestAnimationFrame(render);
    };

    const handleMotionChange = () => {
      cancelAnimationFrame(animationFrame);
      lastFrame = 0;
      animationFrame = requestAnimationFrame(render);
    };

    const interactiveRoot = canvas.closest("button");
    const handlePointerEnter = () => {
      hoverTarget = 1;
    };
    const handlePointerMove = (event: PointerEvent) => {
      const bounds = canvas.getBoundingClientRect();
      pointerTargetX = clamp(
        (event.clientX - bounds.left - bounds.width / 2) / (bounds.width / 2),
        -1,
        1,
      );
      pointerTargetY = clamp(
        (event.clientY - bounds.top - bounds.height / 2) / (bounds.height / 2),
        -1,
        1,
      );
    };
    const handlePointerLeave = () => {
      hoverTarget = 0;
      pointerTargetX = 0;
      pointerTargetY = 0;
    };

    resize();
    draw(performance.now());
    const resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(canvas);
    motionQuery.addEventListener("change", handleMotionChange);
    interactiveRoot?.addEventListener("pointerenter", handlePointerEnter);
    interactiveRoot?.addEventListener("pointermove", handlePointerMove);
    interactiveRoot?.addEventListener("pointerleave", handlePointerLeave);
    animationFrame = requestAnimationFrame(render);

    return () => {
      cancelAnimationFrame(animationFrame);
      resizeObserver.disconnect();
      motionQuery.removeEventListener("change", handleMotionChange);
      interactiveRoot?.removeEventListener("pointerenter", handlePointerEnter);
      interactiveRoot?.removeEventListener("pointermove", handlePointerMove);
      interactiveRoot?.removeEventListener("pointerleave", handlePointerLeave);
    };
  }, []);

  return (
    <span className={styles.orb} aria-hidden="true">
      <canvas className={styles.orbCanvas} ref={canvasRef} />
    </span>
  );
}
