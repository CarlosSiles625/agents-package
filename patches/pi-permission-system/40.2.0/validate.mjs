// CHK-T1-APPLY / CHK-T1-TS: only private copies under /tmp/opencode are patched.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = fileURLToPath(new URL(".", import.meta.url));
const args = process.argv.slice(2);
assert.ok(args.length === 1 && ["--apply-only", "--test"].includes(args[0]), "use --apply-only or --test");
const sha = data => createHash("sha256").update(data).digest("hex");
const checksums = readFileSync(join(dir, "SHA256SUMS"), "utf8").trim().split("\n").map(line => line.split(/\s+/));
for (const [expected, file] of checksums) assert.equal(sha(readFileSync(join(dir, file))), expected, file);
const tmp = mkdtempSync("/tmp/opencode/pi-parser-40.2.0-");
try {
  mkdirSync(join(tmp, "src/config"), { recursive: true });
  const source = join(tmp, "src/config/yaml-frontmatter.ts");
  const original = readFileSync(join(dir, "upstream/yaml-frontmatter.ts"));
  writeFileSync(source, original);
  writeFileSync(join(tmp, "upstream.ts"), original);
  execFileSync("patch", ["--batch", "--forward", "--fuzz=0", "-p1", "--input", join(dir, "patched-40.2.0-v1.patch")], { cwd: tmp, stdio: "inherit" });
  const patched = readFileSync(join(dir, "patched/yaml-frontmatter.ts"));
  assert.deepEqual(readFileSync(source), patched, "applied diff must exactly match pinned patched bytes");
  console.log(`CHK-T1-APPLY PASS original=${sha(original)} patched=${sha(patched)} ephemeral=${tmp}`);
  if (args[0] === "--test") {
    execFileSync(process.execPath, ["--experimental-strip-types", "--test", join(dir, "regression.ts")], {
      cwd: tmp, stdio: "inherit", env: { ...process.env, T21_PATCHED_PARSER: source, T21_UPSTREAM_PARSER: join(tmp, "upstream.ts") },
    });
  }
} finally {
  // Only the private directory this invocation just created is removed.
  rmSync(tmp, { recursive: true, force: true });
}
