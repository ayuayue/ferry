import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

// The helper only exists on Windows: it registers the engine service through
// the SCM and carries a requireAdministrator manifest. Other targets have no
// equivalent, and the Linux packaging step would only ship dead weight.
if (process.platform !== "win32") {
  console.log("skipping the Windows engine helper on " + process.platform);
  process.exit(0);
}

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
