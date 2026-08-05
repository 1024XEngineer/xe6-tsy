/**
 * Ensure sherpa-onnx KWS model weights + WASM binary exist under public/kws.
 * Idempotent: skips downloads when assets are already present.
 *
 * Hooks: npm predev / prebuild (required), postinstall (--optional).
 */
import { copyFileSync, createWriteStream, existsSync, mkdirSync, statSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const destDir = join(__dirname, "..", "public", "kws");
const wasmDir = join(destDir, "wasm");
const modelName = "sherpa-onnx-kws-zipformer-wenetspeech-3.3M-2024-01-01";
const demoDir =
  process.env.LINGOW_KWS_DEMO_MODEL_DIR ||
  join("E:", "poc_demo", "sherpa_onnx_kws_demo", "models", modelName);

const optional =
  process.argv.includes("--optional") ||
  process.env.LINGOW_SKIP_KWS_SYNC === "1";

const modelFiles = [
  ["encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "encoder.onnx"],
  ["decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "decoder.onnx"],
  ["joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "joiner.onnx"],
];

const WASM_CDN =
  "https://cdn.jsdelivr.net/npm/@siteed/sherpa-onnx.rn@1.3.1/wasm";

/** Runtime-required files not committed to git (large binaries). */
const requiredRuntimeFiles = [
  join(destDir, "encoder.onnx"),
  join(destDir, "decoder.onnx"),
  join(destDir, "joiner.onnx"),
  join(wasmDir, "sherpa-onnx-wasm-combined.wasm"),
];

/** Small JS glue — prefer repo copies; download only if missing. */
const wasmJsFiles = [
  "sherpa-onnx-wasm-combined.js",
  "sherpa-onnx-core.js",
  "sherpa-onnx-kws.js",
];

function fileOk(path, minBytes = 64) {
  try {
    return existsSync(path) && statSync(path).size >= minBytes;
  } catch {
    return false;
  }
}

function allReady() {
  return requiredRuntimeFiles.every((path) => fileOk(path, 1024));
}

function copyModelsFrom(dir) {
  for (const [srcName, destName] of modelFiles) {
    const src = join(dir, srcName);
    const dest = join(destDir, destName);
    if (fileOk(dest, 1024)) {
      console.log(`[kws] skip existing ${destName}`);
      continue;
    }
    if (!existsSync(src)) {
      throw new Error(`missing ${src}`);
    }
    copyFileSync(src, dest);
    console.log(`[kws] copied ${destName}`);
  }
  const tokensSrc = join(dir, "tokens.txt");
  const tokensDest = join(destDir, "tokens.txt");
  if (!fileOk(tokensDest) && existsSync(tokensSrc)) {
    copyFileSync(tokensSrc, tokensDest);
    console.log("[kws] copied tokens.txt");
  }
}

async function downloadFile(url, out) {
  const res = await fetch(url);
  if (!res.ok || !res.body) {
    throw new Error(`download failed ${url}: HTTP ${res.status}`);
  }
  await pipeline(res.body, createWriteStream(out));
}

async function downloadAndExtractModels() {
  if (
    fileOk(join(destDir, "encoder.onnx"), 1024) &&
    fileOk(join(destDir, "decoder.onnx"), 1024) &&
    fileOk(join(destDir, "joiner.onnx"), 1024)
  ) {
    console.log("[kws] onnx models already present");
    return;
  }

  const url =
    "https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/" +
    `${modelName}.tar.bz2`;
  const archive = join(tmpdir(), `${modelName}.tar.bz2`);
  console.log(`[kws] downloading ${url}`);
  await downloadFile(url, archive);

  const extractRoot = join(tmpdir(), "lingow-kws-extract");
  mkdirSync(extractRoot, { recursive: true });
  execFileSync("tar", ["-xjf", archive, "-C", extractRoot], {
    stdio: "inherit",
  });
  copyModelsFrom(join(extractRoot, modelName));
}

async function syncWasm() {
  mkdirSync(wasmDir, { recursive: true });

  const wasmBin = join(wasmDir, "sherpa-onnx-wasm-combined.wasm");
  if (!fileOk(wasmBin, 1024 * 1024)) {
    console.log("[kws] downloading sherpa-onnx-wasm-combined.wasm");
    await downloadFile(`${WASM_CDN}/sherpa-onnx-wasm-combined.wasm`, wasmBin);
    console.log("[kws] wrote sherpa-onnx-wasm-combined.wasm");
  } else {
    console.log("[kws] skip existing sherpa-onnx-wasm-combined.wasm");
  }

  for (const name of wasmJsFiles) {
    const out = join(wasmDir, name);
    if (fileOk(out)) {
      console.log(`[kws] skip existing ${name}`);
      continue;
    }
    console.log(`[kws] downloading wasm/${name}`);
    await downloadFile(`${WASM_CDN}/${name}`, out);
    console.log(`[kws] wrote ${name}`);
  }
}

async function main() {
  if (allReady()) {
    console.log("[kws] assets already ready");
    return;
  }

  mkdirSync(destDir, { recursive: true });
  mkdirSync(wasmDir, { recursive: true });

  if (existsSync(join(demoDir, modelFiles[0][0]))) {
    console.log(`[kws] using demo model dir: ${demoDir}`);
    copyModelsFrom(demoDir);
  } else {
    await downloadAndExtractModels();
  }

  await syncWasm();

  if (!allReady()) {
    throw new Error(
      "[kws] sync finished but required assets are still missing under public/kws",
    );
  }
  console.log(`[kws] assets ready in ${destDir}`);
}

try {
  await main();
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  if (optional) {
    console.warn(`[kws] sync skipped (${message}); will retry on npm run dev`);
    process.exit(0);
  }
  console.error(`[kws] sync failed: ${message}`);
  process.exit(1);
}
