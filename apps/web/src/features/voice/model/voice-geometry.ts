export function idleRingRadius(angle: number, elapsed: number) {
  const time = elapsed * 0.00035;

  return (
    1 +
    Math.sin(angle * 3 + time) * 0.08 +
    Math.sin(angle * 7 - time * 1.4) * 0.045 +
    Math.cos(angle * 11 + time * 0.7) * 0.025
  );
}

export function activeAmplitude(elapsed: number) {
  const breath = 0.82 + Math.sin(elapsed * 0.0014) * 0.1;
  const pulse = Math.pow(Math.max(0, Math.sin(elapsed * 0.0026)), 5) * 0.28;

  return Math.min(1.3, breath + pulse);
}
