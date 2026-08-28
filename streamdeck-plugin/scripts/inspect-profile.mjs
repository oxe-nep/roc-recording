import { execSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const profile = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
  "com.nep.commentator.sdPlugin",
  "profiles",
  "commentator.streamDeckProfile",
);
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sdprof-"));
const zip = path.join(tmp, "p.zip");
const out = path.join(tmp, "out");
fs.copyFileSync(profile, zip);
if (process.platform === "win32") {
  execSync(`powershell -NoProfile -Command "Expand-Archive -Path '${zip}' -DestinationPath '${out}' -Force"`, {
    stdio: "inherit",
  });
} else {
  execSync(`unzip -q '${zip}' -d '${out}'`, { stdio: "inherit" });
}

function walk(dir, rel = "") {
  for (const name of fs.readdirSync(dir)) {
    const fp = path.join(dir, name);
    const r = rel ? `${rel}/${name}` : name;
    if (fs.statSync(fp).isDirectory()) walk(fp, r);
    else if (name === "manifest.json") {
      console.log(`--- ${r} ---`);
      console.log(fs.readFileSync(fp, "utf8"));
    }
  }
}
walk(out);
