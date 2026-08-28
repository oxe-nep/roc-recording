import * as esbuild from "esbuild";
import { execSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const watch = process.argv.includes("--watch");
const root = path.dirname(fileURLToPath(import.meta.url));
const pluginRoot = path.join(root, "com.nep.commentator.sdPlugin");
const binDir = path.join(pluginRoot, "bin");
const binPackageJson = path.join(binDir, "package.json");

function preparePluginDeps() {
  execSync("npm install --omit=dev --no-audit --no-fund", {
    cwd: pluginRoot,
    stdio: "inherit",
  });
}

function writeBinPackageJson() {
  fs.mkdirSync(binDir, { recursive: true });
  fs.writeFileSync(binPackageJson, JSON.stringify({ type: "module" }, null, 2) + "\n");
}

const ctx = await esbuild.context({
  entryPoints: ["src/plugin.ts"],
  bundle: true,
  outfile: path.join(binDir, "plugin.js"),
  platform: "node",
  format: "esm",
  target: "node20",
  external: ["@elgato/streamdeck"],
  sourcemap: true,
  logLevel: "info",
});

async function build() {
  preparePluginDeps();
  writeBinPackageJson();
  await ctx.rebuild();
}

if (watch) {
  writeBinPackageJson();
  await ctx.watch();
} else {
  await build();
  await ctx.dispose();
}
