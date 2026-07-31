# Lingow Motion Polish Design

## Objective

Refine the existing single-viewport Lingow demo into a more tactile voice appliance without adding ASR, TTS, LLM, navigation, settings, or additional primary actions.

The interface keeps one central voice control and one latest bilingual subtitle. Motion communicates the session state:

- Idle: a living radial voice ring.
- Activation: sound rings expand horizontally from the control.
- Listening and active translation: the ring resolves into flowing horizontal strands.
- New text: status and subtitles enter with restrained spring motion.

## Visual Direction

The reference image defines shape, not monochrome color. Lingow retains its restrained violet, aqua, and rose voice spectrum against porcelain light and graphite dark surfaces.

The memorable element is a single procedural voice object that changes topology between a hollow radial ring and horizontal strands. Background and typography support that object instead of competing with it.

## Voice States

### Idle Ring

Replace the glass sphere with a Canvas-rendered hollow ring made from approximately 160 radial filaments.

- The inner opening remains stable and readable as a circular control.
- The outer edge uses layered sine fields to create a continuously changing irregular silhouette.
- Motion is slow and low amplitude, resembling breathing rather than active speech.
- Filaments use low-opacity aqua, violet, and rose interpolation.
- The visible ring remains centered inside the existing large button hit area.

### Activation Burst

When the session changes from idle to listening, render three elliptical rings behind the voice object.

- Rings originate close to the center and expand mainly on the horizontal axis.
- Each ring is delayed by 90-120 ms.
- Opacity falls to zero during expansion.
- The burst runs once for approximately 900 ms and does not loop.
- The idle ring crossfades and contracts while the horizontal strands expand from the center.

Ending the session returns directly to the idle ring with a soft scale and opacity transition; it does not replay the outward burst.

### Active Strands

Retain the existing Canvas strand renderer and refine its amplitude model.

- Multiple strands move at slightly different phases and speeds.
- A slow breathing envelope prevents the visual from feeling like a repeated fixed loop.
- A simulated voice impulse periodically increases the center amplitude until real microphone amplitude is connected.
- Color stays most concentrated at the center and fades toward both ends.

No browser microphone permission is requested in this UI-only phase.

## Background Light Field

Add a restrained full-viewport Linear-inspired light field using CSS layers.

- Light mode: cool porcelain base, faint diagonal illumination, extremely subtle perspective grid lines.
- Dark mode: graphite base, low-opacity violet-blue horizon light, faint structural lines.
- The field is full-bleed and non-interactive, with no discrete gradient orbs or floating decorations.
- Contrast remains low enough that the central voice object and subtitles are always the first visual read.

## Typography Motion

Use Motion for semantic text entry rather than character-by-character animation.

- Brand header: opacity and 8 px upward entrance on initial load.
- Status: keyed by session phase; old text fades out, new text rises 6 px and settles with a light spring.
- Latest subtitle: source line enters first, translation follows after 70 ms.
- History overlay: heading and visible turns use a short stagger on opening.
- Text never scales enough to cause layout reflow or clipping.

## Component Changes

- `AuroraOrb` becomes a client Canvas component that renders the radial filament ring.
- `VoiceControl` coordinates idle/active crossfade and mounts a one-shot `ActivationRings` layer.
- `AuroraStrands` adds a simulated amplitude envelope without React state updates per frame.
- `VoiceExperience` animates the brand and status changes.
- `LatestTranslation` animates each new bilingual turn by `turn.id`.
- `HistoryOverlay` adds a restrained opening sequence.
- `voice.module.css` and `globals.css` add the full-viewport light field, ring layers, and responsive motion styling.

Continuous Canvas values remain inside `requestAnimationFrame` refs. They do not trigger React re-renders.

## Accessibility And Performance

- Preserve the existing single semantic button and accessible labels.
- Preserve keyboard focus, Escape-close history, theme controls, and the one-viewport layout.
- Respect `prefers-reduced-motion`: draw static representative ring and strand frames, disable activation expansion, and reduce text transitions to opacity only.
- Cap Canvas device pixel ratio at 2 and animation rate at 30 fps.
- Cache layout dimensions through `ResizeObserver`; do not read layout on every frame.
- Keep all decorative Canvas and ring layers hidden from assistive technology.

## Verification

- Component tests verify the single primary entry point and state labels remain intact.
- Add a component assertion for the idle voice ring and the activation ring layer after starting.
- Playwright verifies no page scrolling at desktop and mobile sizes.
- Playwright captures light idle, light active, dark active, and mobile history states.
- Screenshot review checks that the idle outline is visibly irregular, the active form is horizontal, the background remains restrained, and no elements overlap.
- Run unit tests, ESLint, TypeScript, production build, and the complete Playwright suite.

## Out Of Scope

- Real microphone amplitude
- ASR, translation, TTS, or LLM integration
- Language configuration
- Settings or navigation
- Persistent conversation storage
