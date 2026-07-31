import { describe, expect, it } from "vitest";

import { activeAmplitude, idleRingRadius } from "./voice-geometry";

describe("voice geometry", () => {
  it("keeps the idle ring irregular but bounded", () => {
    const samples = Array.from({ length: 180 }, (_, index) =>
      idleRingRadius((index / 180) * Math.PI * 2, 1200),
    );

    expect(Math.min(...samples)).toBeGreaterThanOrEqual(0.78);
    expect(Math.max(...samples)).toBeLessThanOrEqual(1.22);
    expect(
      new Set(samples.map((value) => value.toFixed(3))).size,
    ).toBeGreaterThan(20);
  });

  it("adds periodic active impulses without exceeding the visual range", () => {
    const quiet = activeAmplitude(0);
    const speaking = activeAmplitude(1050);

    expect(quiet).toBeGreaterThanOrEqual(0.72);
    expect(speaking).toBeLessThanOrEqual(1.3);
    expect(speaking).not.toBe(quiet);
  });
});
