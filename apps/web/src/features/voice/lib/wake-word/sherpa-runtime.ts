/**
 * Loads sherpa-onnx WASM from same-origin /kws/wasm/ (avoids Edge Tracking
 * Prevention blocking third-party CDN storage for jsDelivr scripts).
 */

const WASM_BASE = "/kws/wasm/";

export type SherpaKwsSpotter = {
  handle: number;
  createStream: () => SherpaKwsStream;
  isReady: (stream: SherpaKwsStream) => boolean;
  decode: (stream: SherpaKwsStream) => void;
  reset: (stream: SherpaKwsStream) => void;
  getResult: (stream: SherpaKwsStream) => { keyword?: string };
  free: () => void;
};

export type SherpaKwsStream = {
  handle: number;
  acceptWaveform: (sampleRate: number, samples: Float32Array) => void;
  free: () => void;
};

type EmscriptenFS = {
  mkdir: (path: string) => void;
  writeFile: (path: string, data: Uint8Array | string) => void;
  analyzePath: (path: string) => { exists: boolean };
};

type WasmModule = {
  FS?: EmscriptenFS;
  HEAPU8?: Uint8Array;
  HEAPF32?: Float32Array;
  wasmMemory?: WebAssembly.Memory;
  calledRun?: boolean;
  locateFile?: (path: string, prefix?: string) => string;
  onRuntimeInitialized?: () => void;
};

type SherpaOnnxNamespace = {
  KWS?: {
    loadModel?: (config: object) => Promise<object>;
    createKeywordSpotter?: (
      loadedModel: object,
      options?: object,
    ) => SherpaKwsSpotter;
  };
};

type SherpaWindow = Window & {
  Module?: WasmModule;
  HEAPU8?: Uint8Array;
  HEAPF32?: Float32Array;
  wasmMemory?: WebAssembly.Memory;
  SherpaOnnx?: SherpaOnnxNamespace;
  Kws?: new (config: object, module: unknown) => SherpaKwsSpotter;
  createKws?: (module: unknown, config: object) => SherpaKwsSpotter;
  _lingowSherpaReady?: Promise<boolean>;
};

function getWin(): SherpaWindow {
  return window as SherpaWindow;
}

function loadScript(url: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(
      `script[src="${url}"]`,
    );
    if (existing) {
      if (existing.dataset.lingowLoaded === "1") {
        resolve();
        return;
      }
      const onLoad = () => {
        existing.dataset.lingowLoaded = "1";
        resolve();
      };
      const onError = () => reject(new Error(`Failed to load ${url}`));
      existing.addEventListener("load", onLoad, { once: true });
      existing.addEventListener("error", onError, { once: true });
      return;
    }

    const script = document.createElement("script");
    script.src = url;
    script.async = false;
    script.onload = () => {
      script.dataset.lingowLoaded = "1";
      resolve();
    };
    script.onerror = () => reject(new Error(`Failed to load ${url}`));
    document.head.appendChild(script);
  });
}

function hasKwsFactory(win: SherpaWindow): boolean {
  return (
    typeof win.createKws === "function" ||
    typeof win.Kws === "function" ||
    typeof win.SherpaOnnx?.KWS?.createKeywordSpotter === "function"
  );
}

/**
 * Emscripten keeps HEAP views as classic-script globals and on Module.
 * Never replace window.Module with a spread copy — that can desync the glue
 * binding from window.Module so FS exists but HEAPU8 looks missing.
 */
function syncHeapViews(win: SherpaWindow): boolean {
  const mod = win.Module;
  if (!mod) return false;

  const globalHeap = globalThis as typeof globalThis & {
    HEAPU8?: Uint8Array;
    HEAPF32?: Float32Array;
    HEAP8?: Int8Array;
    wasmMemory?: WebAssembly.Memory;
    Module?: WasmModule;
  };

  // Prefer whatever object Emscripten actually mutated.
  const glueModule =
    globalHeap.Module && globalHeap.Module !== mod ? globalHeap.Module : null;
  if (glueModule?.FS && glueModule !== mod) {
    // Point window.Module at the glue's Module so later calls share HEAP/FS.
    win.Module = glueModule;
    return syncHeapViews(win);
  }

  const readU8 =
    mod.HEAPU8 ||
    (mod as Record<string, unknown>)["HEAPU8"] ||
    win.HEAPU8 ||
    globalHeap.HEAPU8;
  const readF32 =
    mod.HEAPF32 ||
    (mod as Record<string, unknown>)["HEAPF32"] ||
    win.HEAPF32 ||
    globalHeap.HEAPF32;

  if (readU8 instanceof Uint8Array) mod.HEAPU8 = readU8;
  if (readF32 instanceof Float32Array) mod.HEAPF32 = readF32;

  const memory =
    mod.wasmMemory ||
    win.wasmMemory ||
    globalHeap.wasmMemory ||
    (mod as Record<string, unknown>)["wasmMemory"];
  if (memory && typeof memory === "object" && "buffer" in memory) {
    const buffer = (memory as WebAssembly.Memory).buffer;
    if (!mod.HEAPU8) mod.HEAPU8 = new Uint8Array(buffer);
    if (!mod.HEAPF32) mod.HEAPF32 = new Float32Array(buffer);
  }

  return !!(mod.HEAPU8 && mod.HEAPF32);
}

function runtimeGaps(win: SherpaWindow): string[] {
  syncHeapViews(win);
  const gaps: string[] = [];
  if (!win.Module?.FS) gaps.push("Module.FS");
  if (!win.SherpaOnnx) gaps.push("SherpaOnnx");
  if (!hasKwsFactory(win)) gaps.push("createKws|Kws|SherpaOnnx.KWS");
  // HEAP views are synced best-effort; createKeywordSpotter validates them.
  return gaps;
}

function isRuntimeReady(win: SherpaWindow): boolean {
  return runtimeGaps(win).length === 0;
}

/**
 * Idempotent: loads same-origin WASM binary, then core + KWS JS only.
 * Does not use sherpa-onnx-combined.js (it double-inits and false-timeouts).
 */
export function ensureSherpaKwsRuntime(): Promise<boolean> {
  if (typeof window === "undefined") {
    return Promise.resolve(false);
  }
  const win = getWin();
  if (isRuntimeReady(win)) {
    return Promise.resolve(true);
  }
  if (win._lingowSherpaReady) {
    return win._lingowSherpaReady;
  }

  win._lingowSherpaReady = (async () => {
    // Mutate Module in place so Emscripten's `var Module = ...` binding and
    // window.Module stay the same object after HEAP views are attached.
    if (!win.Module) {
      win.Module = {};
    }
    const mod = win.Module;
    const prevOnInit = mod.onRuntimeInitialized;

    let settled = false;
    const runtimeReady = new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        if (settled) return;
        settled = true;
        reject(new Error("WASM runtime init timed out after 120000ms"));
      }, 120_000);

      mod.locateFile = (path: string) => {
        const name = path.split("/").pop() ?? path;
        return `${WASM_BASE}${name}`;
      };
      mod.onRuntimeInitialized = () => {
        try {
          prevOnInit?.();
        } finally {
          syncHeapViews(win);
          if (settled) return;
          settled = true;
          window.clearTimeout(timer);
          resolve();
        }
      };
    });

    await loadScript(`${WASM_BASE}sherpa-onnx-wasm-combined.js`);

    if (!win.Module?.FS || !syncHeapViews(win)) {
      await runtimeReady;
    }

    syncHeapViews(win);

    if (!win.Module?.FS) {
      throw new Error("WASM Module.FS unavailable after runtime init");
    }

    await loadScript(`${WASM_BASE}sherpa-onnx-core.js`);
    await loadScript(`${WASM_BASE}sherpa-onnx-kws.js`);

    if (!hasKwsFactory(win)) {
      await new Promise<void>((r) => window.setTimeout(r, 0));
    }

    syncHeapViews(win);

    const gaps = runtimeGaps(win);
    if (gaps.length > 0) {
      throw new Error(
        `sherpa-onnx KWS runtime incomplete after script load: missing ${gaps.join(", ")}`,
      );
    }
    if (!syncHeapViews(win)) {
      const modKeys = win.Module ? Object.getOwnPropertyNames(win.Module).slice(0, 40) : [];
      throw new Error(
        "WASM HEAP views missing after runtime init. " +
          `Module keys=${modKeys.join(",") || "none"}; ` +
          `global HEAPU8=${String(!!(globalThis as { HEAPU8?: unknown }).HEAPU8)} ` +
          `wasmMemory=${String(!!(globalThis as { wasmMemory?: unknown }).wasmMemory)}`,
      );
    }
    return true;
  })().catch((error) => {
    win._lingowSherpaReady = undefined;
    console.error("[wake-word] sherpa-onnx runtime failed:", error);
    return false;
  });

  return win._lingowSherpaReady;
}

async function fetchIntoFs(
  url: string,
  dest: string,
  fs: EmscriptenFS,
): Promise<void> {
  try {
    if (fs.analyzePath(dest).exists) return;
  } catch {
    // path missing
  }
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`Failed to fetch ${url}: HTTP ${res.status}`);
  }
  const raw = await res.arrayBuffer();
  const data = new Uint8Array(raw.byteLength);
  data.set(new Uint8Array(raw));
  fs.writeFile(dest, data);
}

function ensureDir(fs: EmscriptenFS, path: string): void {
  const parts = path.split("/").filter(Boolean);
  let current = "";
  for (const part of parts) {
    current += `/${part}`;
    try {
      fs.mkdir(current);
    } catch {
      // exists
    }
  }
}

export type CreateSpotterOptions = {
  modelBaseUrl?: string;
  keywordsScore?: number;
  keywordsThreshold?: number;
};

export async function createKeywordSpotter(
  options: CreateSpotterOptions = {},
): Promise<SherpaKwsSpotter> {
  const win = getWin();
  syncHeapViews(win);
  const wasmModule = win.Module;
  if (!wasmModule?.FS) {
    throw new Error("sherpa-onnx WASM runtime is not ready");
  }
  if (!wasmModule.HEAPU8 || !wasmModule.HEAPF32) {
    throw new Error(
      "WASM memory views missing (HEAPU8/HEAPF32). Hard-refresh and ensure /kws/wasm/*.wasm is served.",
    );
  }

  const modelDir = "/lingow-kws";
  const base = (options.modelBaseUrl ?? "/kws").replace(/\/$/, "");
  ensureDir(wasmModule.FS, modelDir);

  const keywordsRes = await fetch(`${base}/keywords.txt`);
  if (!keywordsRes.ok) {
    throw new Error(`Failed to fetch keywords.txt: HTTP ${keywordsRes.status}`);
  }
  const keywordsText = (await keywordsRes.text()).trim();
  if (!keywordsText) {
    throw new Error("keywords.txt is empty");
  }

  await Promise.all([
    fetchIntoFs(
      `${base}/encoder.onnx`,
      `${modelDir}/encoder.onnx`,
      wasmModule.FS,
    ),
    fetchIntoFs(
      `${base}/decoder.onnx`,
      `${modelDir}/decoder.onnx`,
      wasmModule.FS,
    ),
    fetchIntoFs(
      `${base}/joiner.onnx`,
      `${modelDir}/joiner.onnx`,
      wasmModule.FS,
    ),
    fetchIntoFs(`${base}/tokens.txt`, `${modelDir}/tokens.txt`, wasmModule.FS),
  ]);

  const configObj = {
    featConfig: {
      samplingRate: 16000,
      featureDim: 80,
    },
    modelConfig: {
      transducer: {
        encoder: `${modelDir}/encoder.onnx`,
        decoder: `${modelDir}/decoder.onnx`,
        joiner: `${modelDir}/joiner.onnx`,
      },
      tokens: `${modelDir}/tokens.txt`,
      provider: "cpu",
      modelType: "",
      numThreads: 1,
      debug: 0,
      modelingUnit: "",
      bpeVocab: "",
    },
    maxActivePaths: 4,
    numTrailingBlanks: 1,
    keywordsScore: options.keywordsScore ?? 2.2,
    keywordsThreshold: options.keywordsThreshold ?? 0.08,
    keywords: keywordsText,
  };

  let spotter: SherpaKwsSpotter | null = null;
  try {
    if (typeof win.createKws === "function") {
      spotter = win.createKws(wasmModule, configObj);
    } else if (typeof win.Kws === "function") {
      spotter = new win.Kws(configObj, wasmModule);
    } else {
      throw new Error("no KWS factory on window");
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`KeywordSpotter init failed: ${message}`);
  }

  if (!spotter?.handle) {
    throw new Error(
      "Failed to create KeywordSpotter (null handle) — check model files under /kws/",
    );
  }
  return spotter;
}
