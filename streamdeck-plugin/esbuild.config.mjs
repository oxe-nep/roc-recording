import * as esbuild from "esbuild";

const watch = process.argv.includes("--watch");

const ctx = await esbuild.context({
  entryPoints: ["src/plugin.ts"],
  bundle: true,
  outfile: "com.nep.commentator.sdPlugin/bin/plugin.js",
  platform: "node",
  format: "esm",
  target: "node20",
  external: ["@elgato/streamdeck"],
  sourcemap: true,
  logLevel: "info",
});

if (watch) {
  await ctx.watch();
} else {
  await ctx.rebuild();
  await ctx.dispose();
}
