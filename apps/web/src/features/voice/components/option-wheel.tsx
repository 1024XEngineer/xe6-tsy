"use client";

import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import styles from "../voice.module.css";

type Side = "left" | "right";

export interface OptionWheelProps {
  items: string[];
  defaultSelected?: number;
  onChange?: (index: number, item: string) => void;
  textColor?: string;
  activeColor?: string;
  side?: Side;
  fontSize?: number;
  spacing?: number;
  curve?: number;
  tilt?: number;
  blur?: number;
  fade?: number;
  minOpacity?: number;
  smoothing?: number;
  inset?: number;
  loop?: boolean;
  draggable?: boolean;
}

interface WheelConfig {
  count: number;
  items: string[];
  rowH: number;
  curve: number;
  tilt: number;
  blur: number;
  fade: number;
  minOpacity: number;
  side: Side;
  loop: boolean;
  smoothing: number;
  draggable: boolean;
}

export function OptionWheel({
  items,
  defaultSelected = 0,
  onChange,
  textColor = "#747471",
  activeColor = "#f5f5f2",
  side = "left",
  fontSize = 1.38,
  spacing = 1.72,
  curve = 0.72,
  tilt = 7,
  blur = 0.8,
  fade = 0.18,
  minOpacity = 0.08,
  smoothing = 240,
  inset = 28,
  loop = false,
  draggable = true,
}: OptionWheelProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<(HTMLDivElement | null)[]>([]);
  const positionRef = useRef(defaultSelected);
  const targetRef = useRef(defaultSelected);
  const animationRef = useRef<number | null>(null);
  const lastFrameRef = useRef(0);
  const configRef = useRef<WheelConfig>({
    count: items.length,
    items,
    rowH: Math.max(fontSize * spacing * 16, 1),
    curve,
    tilt,
    blur,
    fade,
    minOpacity,
    side,
    loop,
    smoothing,
    draggable,
  });
  const onChangeRef = useRef(onChange);
  const runFrameRef = useRef<(now: number) => void>(() => undefined);
  const selectedRef = useRef(defaultSelected);
  const wheelTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const dragRef = useRef<{ y: number; start: number; id: number } | null>(null);
  const dragMovedRef = useRef(false);
  const [selectedIndex, setSelectedIndex] = useState(defaultSelected);
  const [isDragging, setIsDragging] = useState(false);
  const runFrame = useCallback((now: number) => {
    const deltaTime = Math.min((now - lastFrameRef.current) / 1000, 0.05);
    lastFrameRef.current = now;
    const config = configRef.current;
    const smoothingTime = Math.max(config.smoothing, 1) / 1000;
    const easing = 1 - Math.exp(-deltaTime / smoothingTime);
    const target = targetRef.current;
    const current = positionRef.current;
    let next = current + (target - current) * easing;
    const settled = Math.abs(target - next) < 0.001;

    if (settled) next = target;
    positionRef.current = next;

    const mirror = config.side === "right" ? -1 : 1;
    const tiltRadians = (config.tilt * Math.PI) / 180;
    const radius = tiltRadians > 0.0005 ? config.rowH / tiltRadians : 0;

    for (let index = 0; index < config.count; index += 1) {
      const element = itemRefs.current[index];
      if (!element) continue;

      let distanceFromSelection = index - next;
      if (config.loop && config.count > 1) {
        distanceFromSelection =
          ((distanceFromSelection % config.count) + config.count) %
          config.count;
        if (distanceFromSelection > config.count / 2) {
          distanceFromSelection -= config.count;
        }
      }

      const distance = Math.abs(distanceFromSelection);
      let x = 0;
      let y = distanceFromSelection * config.rowH;
      let rotation = 0;

      if (radius > 0) {
        const angle = Math.max(
          -Math.PI / 2,
          Math.min(Math.PI / 2, distanceFromSelection * tiltRadians),
        );
        y = radius * Math.sin(angle);
        x = -mirror * radius * (1 - Math.cos(angle)) * config.curve;
        rotation = (mirror * angle * 180) / Math.PI;
      }

      element.style.transform = `translate(${x.toFixed(2)}px, calc(${y.toFixed(2)}px - 50%)) rotate(${rotation.toFixed(3)}deg)`;
      element.style.opacity = String(
        Math.max(config.minOpacity, 1 - distance * config.fade),
      );
      element.style.filter =
        config.blur > 0
          ? `blur(${(distance * config.blur).toFixed(2)}px)`
          : "none";
      element.style.setProperty(
        "--ow-progress",
        Math.max(0, 1 - Math.min(distance, 1)).toFixed(4),
      );
    }

    animationRef.current = settled
      ? null
      : requestAnimationFrame((nextNow) => runFrameRef.current(nextNow));
  }, []);

  useEffect(() => {
    runFrameRef.current = runFrame;
  }, [runFrame]);

  const startAnimation = useCallback(() => {
    if (animationRef.current !== null) return;
    lastFrameRef.current = performance.now();
    animationRef.current = requestAnimationFrame(runFrame);
  }, [runFrame]);

  const applyTarget = useCallback(
    (value: number, snap: boolean) => {
      const config = configRef.current;
      let nextValue = value;
      if (!config.loop) {
        nextValue = Math.min(
          Math.max(nextValue, 0),
          Math.max(config.count - 1, 0),
        );
      }
      if (snap) nextValue = Math.round(nextValue);
      targetRef.current = nextValue;

      const index =
        ((Math.round(nextValue) % config.count) + config.count) % config.count;
      if (index !== selectedRef.current) {
        selectedRef.current = index;
        setSelectedIndex(index);
        onChangeRef.current?.(index, config.items[index]);
      }
      startAnimation();
    },
    [startAnimation],
  );

  useEffect(() => {
    const remPx =
      parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    onChangeRef.current = onChange;
    configRef.current = {
      count: items.length,
      items,
      rowH: Math.max(fontSize * spacing * remPx, 1),
      curve,
      tilt,
      blur,
      fade,
      minOpacity,
      side,
      loop,
      smoothing,
      draggable,
    };
  }, [
    items,
    onChange,
    fontSize,
    spacing,
    curve,
    tilt,
    blur,
    fade,
    minOpacity,
    side,
    loop,
    smoothing,
    draggable,
  ]);

  useEffect(() => {
    const element = rootRef.current;
    if (!element) return;

    const handleWheel = (event: WheelEvent) => {
      event.preventDefault();
      const config = configRef.current;
      const delta = event.deltaMode === 1 ? event.deltaY * 24 : event.deltaY;
      const step = Math.max(-1, Math.min(1, delta / config.rowH));
      applyTarget(targetRef.current + step, false);
      if (wheelTimerRef.current) clearTimeout(wheelTimerRef.current);
      wheelTimerRef.current = setTimeout(
        () => applyTarget(targetRef.current, true),
        140,
      );
    };

    element.addEventListener("wheel", handleWheel, { passive: false });
    return () => {
      element.removeEventListener("wheel", handleWheel);
      if (wheelTimerRef.current) clearTimeout(wheelTimerRef.current);
    };
  }, [applyTarget]);

  const handlePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      if (!configRef.current.draggable) return;
      dragRef.current = {
        y: event.clientY,
        start: targetRef.current,
        id: event.pointerId,
      };
      dragMovedRef.current = false;
      setIsDragging(true);
    },
    [],
  );

  const handlePointerMove = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      const drag = dragRef.current;
      if (!drag) return;
      const deltaY = event.clientY - drag.y;
      if (!dragMovedRef.current && Math.abs(deltaY) > 4) {
        dragMovedRef.current = true;
        rootRef.current?.setPointerCapture(drag.id);
      }
      if (dragMovedRef.current) {
        applyTarget(drag.start - deltaY / configRef.current.rowH, false);
      }
    },
    [applyTarget],
  );

  const handlePointerEnd = useCallback(() => {
    if (!dragRef.current) return;
    dragRef.current = null;
    setIsDragging(false);
    if (dragMovedRef.current) applyTarget(targetRef.current, true);
  }, [applyTarget]);

  const handleItemClick = useCallback(
    (index: number) => {
      if (dragMovedRef.current) return;
      const config = configRef.current;
      const current = targetRef.current;
      let distance =
        index - (((current % config.count) + config.count) % config.count);
      if (config.loop && config.count > 1) {
        if (distance > config.count / 2) distance -= config.count;
        else if (distance < -config.count / 2) distance += config.count;
      }
      applyTarget(current + distance, true);
    },
    [applyTarget],
  );

  const handleKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLDivElement>) => {
      let delta: number | null = null;
      if (event.key === "ArrowUp" || event.key === "ArrowLeft") delta = -1;
      else if (event.key === "ArrowDown" || event.key === "ArrowRight") {
        delta = 1;
      }
      if (delta === null) return;
      event.preventDefault();
      applyTarget(Math.round(targetRef.current) + delta, true);
    },
    [applyTarget],
  );

  useEffect(() => {
    applyTarget(targetRef.current, false);
  }, [
    items,
    fontSize,
    spacing,
    curve,
    tilt,
    blur,
    fade,
    minOpacity,
    side,
    loop,
    smoothing,
    applyTarget,
  ]);

  useEffect(
    () => () => {
      if (animationRef.current !== null) {
        cancelAnimationFrame(animationRef.current);
        animationRef.current = null;
      }
    },
    [],
  );

  return (
    <div
      aria-label="设置选项"
      className={`${styles.optionWheel} ${isDragging ? styles.optionWheelDragging : ""}`}
      onKeyDown={handleKeyDown}
      onPointerCancel={handlePointerEnd}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerEnd}
      ref={rootRef}
      role="listbox"
      style={
        {
          "--ow-active-color": activeColor,
          "--ow-font-size": `${fontSize}rem`,
          "--ow-inset": `${inset}px`,
          "--ow-text-color": textColor,
        } as CSSProperties
      }
      tabIndex={0}
    >
      {items.map((label, index) => (
        <div
          aria-selected={selectedIndex === index}
          className={`${styles.optionWheelItem} ${
            side === "right"
              ? styles.optionWheelItemRight
              : styles.optionWheelItemLeft
          } ${selectedIndex === index ? styles.optionWheelItemActive : ""}`}
          key={`${label}-${index}`}
          onClick={() => handleItemClick(index)}
          ref={(element) => {
            itemRefs.current[index] = element;
          }}
          role="option"
        >
          {label}
        </div>
      ))}
    </div>
  );
}
