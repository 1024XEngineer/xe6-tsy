# Lingow Motion Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the static idle sphere with a procedural voice ring, add a one-shot horizontal activation burst, enrich the active strand motion and background light field, and animate semantic text changes while preserving the single-viewport voice workflow.

**Architecture:** Keep continuous drawing inside focused Canvas client components with pure geometry helpers covered by unit tests. Use Motion only for finite state transitions and text entry, CSS for the full-viewport light field, and existing session state as the single source of truth. No microphone or backend APIs are introduced.

**Tech Stack:** Next.js 16, React 19, TypeScript, Canvas 2D, Motion, CSS Modules, Vitest, Testing Library, Playwright.

---

## File Map

- Create `src/features/voice/model/voice-geometry.ts`: pure deterministic radius and amplitude functions shared by Canvas renderers.
- Create `src/features/voice/model/voice-geometry.test.ts`: geometry range and motion tests.
- Create `src/features/voice/components/activation-rings.tsx`: decorative one-shot ring burst.
- Modify `src/features/voice/components/aurora-orb.tsx`: convert idle visual to Canvas radial filaments.
- Modify `src/features/voice/components/aurora-strands.tsx`: use a breathing and simulated impulse amplitude envelope.
- Modify `src/features/voice/components/voice-control.tsx`: coordinate crossfade and activation layer.
- Modify `src/features/voice/components/voice-experience.tsx`: animate header and keyed status text.
- Modify `src/features/voice/components/latest-translation.tsx`: animate keyed bilingual lines.
- Modify `src/features/voice/components/history-overlay.tsx`: animate overlay content entry.
- Modify `src/features/voice/components/voice-experience.test.tsx`: assert state-specific visual layers.
- Modify `src/features/voice/voice.module.css`: voice visual, ring burst, typography motion support, and responsive sizing.
- Modify `src/app/globals.css`: restrained full-viewport light field tokens and layers.
- Modify `e2e/voice-experience.spec.ts`: stabilize visual captures and validate state visuals.

### Task 1: Lock State-Specific Voice Visual Behavior

**Files:**
- Modify: `src/features/voice/components/voice-experience.test.tsx`
- Create: `src/features/voice/components/activation-rings.tsx`
- Modify: `src/features/voice/components/voice-control.tsx`

- [ ] **Step 1: Write the failing component assertion**

Add these assertions to the existing start and transition tests:

```tsx
expect(screen.getByTestId("idle-voice-ring")).toBeInTheDocument();
expect(screen.queryByTestId("active-voice-strands")).toBeNull();

fireEvent.click(screen.getByRole("button", { name: "开始语音会话" }));
expect(screen.getByTestId("activation-rings")).toBeInTheDocument();
expect(screen.getByTestId("active-voice-strands")).toBeInTheDocument();
expect(screen.queryByTestId("idle-voice-ring")).toBeNull();
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
npm test -- --run src/features/voice/components/voice-experience.test.tsx
```

Expected: FAIL because the three test IDs do not exist.

- [ ] **Step 3: Add the one-shot activation layer**

Create `ActivationRings` as a decorative component:

```tsx
import styles from "../voice.module.css";

export function ActivationRings() {
  return (
    <span
      aria-hidden="true"
      className={styles.activationRings}
      data-testid="activation-rings"
    >
      {[0, 1, 2].map((ring) => (
        <span key={ring} style={{ "--ring": ring } as React.CSSProperties} />
      ))}
    </span>
  );
}
```

Update `VoiceControl` to render the idle and active visuals through `AnimatePresence`, label them with the test IDs, and mount `ActivationRings` only while `phase === "listening"`.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run the focused Vitest command. Expected: all `voice-experience` tests pass.

- [ ] **Step 5: Commit the state behavior**

```powershell
git add src/features/voice/components/voice-experience.test.tsx src/features/voice/components/activation-rings.tsx src/features/voice/components/voice-control.tsx
git commit -m "feat: add voice state transition layers"
```

### Task 2: Build Procedural Idle Ring And Active Amplitude

**Files:**
- Create: `src/features/voice/model/voice-geometry.ts`
- Create: `src/features/voice/model/voice-geometry.test.ts`
- Modify: `src/features/voice/components/aurora-orb.tsx`
- Modify: `src/features/voice/components/aurora-strands.tsx`

- [ ] **Step 1: Write failing pure geometry tests**

```ts
import { describe, expect, it } from "vitest";
import { activeAmplitude, idleRingRadius } from "./voice-geometry";

describe("voice geometry", () => {
  it("keeps the idle ring irregular but bounded", () => {
    const samples = Array.from({ length: 180 }, (_, index) =>
      idleRingRadius((index / 180) * Math.PI * 2, 1200),
    );
    expect(Math.min(...samples)).toBeGreaterThanOrEqual(0.78);
    expect(Math.max(...samples)).toBeLessThanOrEqual(1.22);
    expect(new Set(samples.map((value) => value.toFixed(3))).size).toBeGreaterThan(20);
  });

  it("adds periodic active impulses without exceeding the visual range", () => {
    const quiet = activeAmplitude(0);
    const speaking = activeAmplitude(1050);
    expect(quiet).toBeGreaterThanOrEqual(0.72);
    expect(speaking).toBeLessThanOrEqual(1.3);
    expect(speaking).not.toBe(quiet);
  });
});
```

- [ ] **Step 2: Run geometry tests and verify RED**

Run:

```powershell
npm test -- --run src/features/voice/model/voice-geometry.test.ts
```

Expected: FAIL because `voice-geometry.ts` does not exist.

- [ ] **Step 3: Implement deterministic geometry helpers**

```ts
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
```

- [ ] **Step 4: Run geometry tests and verify GREEN**

Expected: both geometry tests pass.

- [ ] **Step 5: Replace the idle sphere with radial filament Canvas**

Make `AuroraOrb` a client component. Render a `canvas` inside `.orb`, cache display dimensions with `ResizeObserver`, draw 160 radial strokes between a stable inner radius and `idleRingRadius()` outer radius, cap DPR at 2 and drawing at 30 fps, and draw one static frame for reduced motion.

- [ ] **Step 6: Apply `activeAmplitude` inside the strand renderer**

Multiply the existing primary and detail wave displacement by `activeAmplitude(currentTime)`. Keep the renderer ref-driven and React-state-free.

- [ ] **Step 7: Run unit tests, typecheck, and lint**

```powershell
npm test
npm run typecheck
npm run lint
```

Expected: 0 failures and 0 errors.

- [ ] **Step 8: Commit procedural voice rendering**

```powershell
git add src/features/voice/model/voice-geometry.ts src/features/voice/model/voice-geometry.test.ts src/features/voice/components/aurora-orb.tsx src/features/voice/components/aurora-strands.tsx
git commit -m "feat: render procedural lingow voice states"
```

### Task 3: Add Background And Semantic Text Motion

**Files:**
- Modify: `src/features/voice/components/voice-experience.tsx`
- Modify: `src/features/voice/components/latest-translation.tsx`
- Modify: `src/features/voice/components/history-overlay.tsx`
- Modify: `src/features/voice/voice.module.css`
- Modify: `src/app/globals.css`

- [ ] **Step 1: Add a failing reduced-motion browser assertion**

Extend Playwright with a reduced-motion context, activate the session, and assert both status and active voice strands remain visible:

```ts
test("keeps voice states legible with reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  const lightField = await page.locator("main").evaluate((element) =>
    getComputedStyle(element, "::before").backgroundImage,
  );
  expect(lightField).not.toBe("none");
  await page.getByRole("button", { name: "开始语音会话" }).click();
  await expect(page.getByTestId("active-voice-strands")).toBeVisible();
  await expect(page.getByText("正在聆听…", { exact: true })).toBeVisible();
});
```

- [ ] **Step 2: Run the focused browser test and verify RED**

Run:

```powershell
npx playwright test -g "reduced motion"
```

Expected: FAIL because `.experience::before` does not yet provide the full-viewport light field.

- [ ] **Step 3: Animate semantic text with Motion**

- Wrap the brand header in an initial opacity and 8 px rise.
- Render status through `AnimatePresence mode="wait"`, keyed by `state.phase`, with opacity and 6 px rise.
- Convert `LatestTranslation` to a client component and animate source then translation with a 70 ms delay.
- Animate history heading and visible turns with a short stagger while retaining focus behavior.

- [ ] **Step 4: Add the restrained full-viewport light field**

Use `.experience::before` and `.experience::after` for a low-opacity diagonal illumination field and structural grid. Add light/dark CSS variables for field color and line opacity. Keep both layers pointer-free, below content, and full-bleed.

- [ ] **Step 5: Add ring and transition CSS**

Implement the three activation rings with horizontal scale emphasis, 90 ms stagger, 900 ms one-shot fade, and a static reduced-motion fallback. Replace sphere styles with Canvas ring sizing and a subtle central shadow.

- [ ] **Step 6: Run the reduced-motion test and verify GREEN**

Expected: the reduced-motion browser test passes with both text and voice state visible.

- [ ] **Step 7: Commit background and typography motion**

```powershell
git add src/app/globals.css src/features/voice/components/voice-experience.tsx src/features/voice/components/latest-translation.tsx src/features/voice/components/history-overlay.tsx src/features/voice/voice.module.css e2e/voice-experience.spec.ts
git commit -m "feat: polish lingow motion and light field"
```

### Task 4: Visual Verification And Final Quality Gate

**Files:**
- Modify if required: `src/features/voice/voice.module.css`
- Modify if required: `src/features/voice/components/aurora-orb.tsx`
- Modify if required: `src/features/voice/components/aurora-strands.tsx`

- [ ] **Step 1: Run the full automated gate**

```powershell
npm test
npm run lint
npm run typecheck
npm run build
npx playwright test
```

Expected: all unit/component tests and all desktop/mobile Playwright tests pass; lint, typecheck, and build exit 0.

- [ ] **Step 2: Inspect visual captures**

Review desktop and mobile screenshots for light idle, light active, dark active, and history. Confirm the idle ring is hollow and irregular, activation does not resize layout, active strands remain horizontal, background light stays below the voice object in contrast, subtitles do not overlap, and no scrollbars appear.

- [ ] **Step 3: Check Canvas pixels**

Use Playwright `canvas.toDataURL()` and center-region screenshot sampling to confirm both idle and active Canvas elements contain non-transparent pixels at desktop and mobile sizes.

- [ ] **Step 4: Re-run affected checks after any visual correction**

Run the complete Playwright suite and the relevant unit test after the final CSS or Canvas adjustment.

- [ ] **Step 5: Commit final corrections**

```powershell
git add src e2e
git commit -m "fix: finalize lingow responsive motion"
```
