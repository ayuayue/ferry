import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
// The Go linker does not automatically embed a sibling .exe.manifest.
// Compile it into the helper package's COFF resource section first.
execFileSync("go", [
  "run", "./scripts/winres",
  "resources/engine-setup.exe.manifest",
  "cmd/engine-setup/engine-setup.syso",
], { cwd: root, stdio: "inherit" });
execFileSync("go", [
  "build", "-trimpath", "-ldflags=-H=windowsgui -s -w",
  "-o", "resources/engine-setup.exe", "./cmd/engine-setup",
], { cwd: root, stdio: "inherit" });
