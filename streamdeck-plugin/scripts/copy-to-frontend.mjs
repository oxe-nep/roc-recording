import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));
const pluginRoot = path.resolve(root, "..");
const src = path.join(pluginRoot, "com.nep.commentator.streamDeckPlugin");
const destDir = path.resolve(pluginRoot, "../frontend/public/downloads");
const dest = path.join(destDir, "com.nep.commentator.streamDeckPlugin");

if (!fs.existsSync(src)) {
  console.error("Pack the plugin first: npm run pack");
  process.exit(1);
}

fs.mkdirSync(destDir, { recursive: true });
fs.copyFileSync(src, dest);
console.log(`Copied plugin to ${dest}`);
