import fs from "node:fs";
import path from "node:path";

const [rootDir, outputPath] = process.argv.slice(2);
if (!rootDir || !outputPath) {
  throw new Error("usage: node generate-coverage-badge.mjs <coverage-root> <output>");
}

const artifacts = [
  "go-coverage-contracts",
  "go-coverage-api",
  "go-coverage-realtime-audio",
];

let totalStatements = 0;
let coveredStatements = 0;

for (const artifact of artifacts) {
  const profilePath = path.join(rootDir, artifact, "coverage.out");
  const content = fs.readFileSync(profilePath, "utf8");

  for (const line of content.split(/\r?\n/)) {
    if (!line || line.startsWith("mode:")) continue;

    const match = line.match(/^(.*):\d+\.\d+,\d+\.\d+\s+(\d+)\s+(\d+)$/);
    if (!match) continue;

    const statements = Number(match[2]);
    const hits = Number(match[3]);
    totalStatements += statements;
    coveredStatements += hits > 0 ? statements : 0;
  }
}

const coverage = totalStatements === 0
  ? 0
  : (coveredStatements / totalStatements) * 100;
const color = coverage >= 80 ? "brightgreen"
  : coverage >= 60 ? "yellow"
  : "red";

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, JSON.stringify({
  schemaVersion: 1,
  label: "coverage",
  message: coverage.toFixed(1) + "%",
  color,
  cacheSeconds: 300,
}));
