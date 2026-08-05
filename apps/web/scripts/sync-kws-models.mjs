/**
 * Sync KWS model weights and same-origin WASM runtime into public/kws.
 */
import { copyFileSync, createWriteStream, existsSync, mkdirSync } from "node:fs";
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

const mapping = [
  ["encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "encoder.onnx"],
  ["decoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "decoder.onnx"],
  ["joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx", "joiner.onnx"],
  ["tokens.txt", "tokens.txt"],
];

const WASM_CDN =
  "https://cdn.jsdelivr.net/npm/@siteed/sherpa-onnx.rn@1.3.1/wasm";
const wasmFiles = [
  "sherpa-onnx-wasm-combined.js",
  "sherpa-onnx-wasm-combined.wasm",
  "sherpa-onnx-core.js",
  "sherpa-onnx-kws.js",
];

mkdirSync(destDir, { recursive: true });
mkdirSync(wasmDir, { recursive: true });

function copyFrom(dir) {
  for (const [srcName, destName] of mapping) {
    const src = join(dir, srcName);
    if (!existsSync(src)) {
      throw new Error(`missing ${src}`);
    }
    copyFileSync(src, join(destDir, destName));
    console.log(`copied ${destName}`);
  }
}

async function downloadAndExtract() {
  const url =
    "https://github.com/k2-fsa/sherpa-onnx/releases/download/kws-models/" +
    `${modelName}.tar.bz2`;
  const archive = join(tmpdir(), `${modelName}.tar.bz2`);
  console.log(`downloading ${url}`);
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`download failed: HTTP ${res.status}`);
  }
  await pipeline(res.body, createWriteStream(archive));

  const extractRoot = join(tmpdir(), "lingow-kws-extract");
  mkdirSync(extractRoot, { recursive: true });
  execFileSync("tar", ["-xjf", archive, "-C", extractRoot], {
    stdio: "inherit",
  });
  copyFrom(join(extractRoot, modelName));
}

async function syncWasm() {
  for (const name of wasmFiles) {
    const out = join(wasmDir, name);
    if (existsSync(out) && name.endsWith(".wasm")) {
      console.log(`skip existing ${name}`);
      continue;
    }
    console.log(`downloading wasm/${name}`);
    const res = await fetch(`${WASM_CDN}/${name}`);
    if (!res.ok) {
      throw new Error(`wasm download failed ${name}: HTTP ${res.status}`);
    }
    await pipeline(res.body, createWriteStream(out));
    console.log(`wrote wasm/${name}`);
  }
}

if (existsSync(join(demoDir, mapping[0][0]))) {
  console.log(`using demo model dir: ${demoDir}`);
  copyFrom(demoDir);
} else {
  await downloadAndExtract();
}

await syncWasm();
console.log(`KWS assets ready in ${destDir}`);
