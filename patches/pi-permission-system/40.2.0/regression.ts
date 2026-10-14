import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import test from "node:test";

// validate.mjs supplies only private ephemeral copies, never installed plugins.
const patchedPath = process.env.T21_PATCHED_PARSER;
const upstreamPath = process.env.T21_UPSTREAM_PARSER;
assert.ok(patchedPath && upstreamPath, "run through validate.mjs --test");
const { parseSimpleYamlMap: parse, extractFrontmatter } = await import(pathToFileURL(patchedPath).href);
const { parseSimpleYamlMap: upstream } = await import(pathToFileURL(upstreamPath).href);

type Rule = { surface: string; pattern: string; action: string };
const source = readFileSync(new URL("../../../agents/git-manager.md", import.meta.url), "utf8");
const sourceRules: Rule[] = [];
let rule: Partial<Rule> = {};
const scalar = (s: string): string => s.startsWith('"') ? JSON.parse(s) : s;
for (const line of extractFrontmatter(source).split(/\r?\n/)) {
  let match = line.match(/^  - action: (.+)$/);
  if (match) {
    rule = { surface: scalar(match[1]) };
    if (rule.surface === "shell") rule.surface = "bash";
  }
  match = line.match(/^    resource: (.+)$/);
  if (match) rule.pattern = scalar(match[1]);
  match = line.match(/^    effect: (.+)$/);
  if (match) {
    rule.action = scalar(match[1]);
    assert.ok(rule.surface && rule.pattern && rule.action);
    sourceRules.push(rule as Rule);
  }
}

const groups = new Map<string, Rule[]>();
for (const r of sourceRules) groups.set(r.surface, [...(groups.get(r.surface) ?? []), r]);
let yaml = "permission:\n";
for (const [surface, rules] of groups) {
  if (surface === "*") yaml += `  "*": ${JSON.stringify(rules.at(-1)!.action)}\n`;
  else {
    yaml += `  ${JSON.stringify(surface)}:\n`;
    for (const r of rules) yaml += `    ${JSON.stringify(r.pattern)}: ${JSON.stringify(r.action)}\n`;
  }
}

function flatten(policy: Record<string, string | Record<string, string>>): Rule[] {
  return Object.entries(policy).flatMap(([surface, value]) => typeof value === "string"
    ? [{ surface, pattern: "*", action: value }]
    : Object.entries(value).map(([pattern, action]) => ({ surface, pattern, action })));
}
function matches(pattern: string, value: string): boolean {
  const regex = pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".");
  return new RegExp(`^${regex}$`, "su").test(value);
}
function evaluate(rules: Rule[], surface: string, value: string): string {
  return rules.findLast(r => matches(r.surface, surface) && matches(r.pattern, value))?.action ?? "ask";
}

test("five actual colon keys, all command/backslash keys and last-match decisions survive", () => {
  const policy = parse(yaml).permission;
  const rules = flatten(policy);
  const colons = sourceRules.filter(r => r.pattern.includes(":"));
  assert.deepEqual(colons.map(r => r.pattern), ["git push *:*", "git add -- :*", "git add -- * :*", "git add :*", "git add * :*"]);
  for (const r of sourceRules) {
    if (r.surface !== "*") assert.equal(policy[r.surface][r.pattern], r.action, r.pattern);
  }
  assert.deepEqual(Object.keys(policy.bash), sourceRules.filter(r => r.surface === "bash").map(r => r.pattern));
  const cases = [
    ["bash", "git push origin HEAD:main", "ask"], ["bash", "git push origin main", "allow"],
    ["bash", "git ls-files agents/git-manager.md", "allow"],
    ["bash", "git ls-files --stage", "allow"], ["bash", "git ls-files; echo unsafe", "ask"],
    ["bash", "git reset --hard", "ask"], ["bash", "git clean -fd", "ask"],
    ["bash", "git add -- :x", "deny"], ["bash", "git add -- a :x", "deny"],
    ["bash", "git add :x", "deny"], ["bash", "git add a :x", "deny"],
    ["bash", "git add -- file.txt", "allow"], ["bash", "git add file.txt", "allow"],
    ["read", "repo/.ssh\\private", "deny"], ["read", "repo/secrets\\token", "deny"],
  ];
  for (const [surface, command, expected] of cases) {
    assert.equal(evaluate(rules, surface, command), expected);
    assert.equal(evaluate(rules, surface, command), evaluate(sourceRules, surface, command));
  }
  const old = upstream(yaml).permission;
  for (const r of colons) assert.notEqual(old.bash[r.pattern], r.action, "upstream must reproduce the regression");
});

test("single/double quote escapes, literal backslashes and Unicode are exact keys", () => {
  const keys = [String.raw`git "a:b" C:\repo\*`, "git 'a:b' *", "clé:日本語🧪", "__proto__"];
  for (const key of keys) {
    const value = parse(`permission:\n  bash:\n    ${JSON.stringify(key)}: deny\n`).permission.bash;
    assert.deepEqual(Object.keys(value), [key]);
    assert.equal(Object.getOwnPropertyDescriptor(value, key)?.value, "deny");
    assert.equal(Object.getPrototypeOf(value), Object.prototype);
  }
  assert.deepEqual(Object.keys(parse("'it''s:C:\\repo\\*': allow").valueOf()), [String.raw`it's:C:\repo\*`]);
  assert.deepEqual(Object.keys(parse('"\\u0061\\u003a\\uD83E\\uDDEA": deny')), ["a:🧪"]);
});

test("nested maps, scalar values, frontmatter and duplicate insertion order stay unchanged", () => {
  const input = '# comment\npermission:\n  read:\n    "*": "deny"\n    "src/*": allow\n    "*": ask\n  edit: deny\nnumber: 42\ntext: "a\\\\b"\n';
  assert.deepEqual(parse(input), upstream(input));
  assert.deepEqual(Object.keys(parse(input).permission.read), ["*", "src/*"]);
  assert.equal(parse(input).text, String.raw`a\\b`);
  assert.equal(extractFrontmatter("---\r\npermission: deny\r\n---\r\nbody"), "permission: deny");
});

test("malformed or ill-formed keys throw instead of returning a partial policy", () => {
  for (const key of ['"unterminated: deny', "'unterminated: deny", '"ok"extra: deny',
    '"bad\\q": deny', '"bad\\": deny', '"": deny', ': deny', 'bad"key: deny',
    '"\\uD800": deny', '"\\uDC00": deny', '"\\uD800x": deny', '"\\u0000": deny',
    '"\\n": deny', "missing-separator"]) {
    assert.throws(() => parse(`permission:\n  read: allow\n  bash:\n    ${key}\n`), SyntaxError, key);
  }
});
