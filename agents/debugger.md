---
description: Diagnose an assigned software failure with the smallest discriminating, evidence-based investigation.
mode: subagent
model: openai/gpt-6.1-sol#medium
steps: 24
permissions:
  - action: "*"
    resource: "*"
    effect: deny
  - action: read
    resource: "*"
    effect: allow
  - action: glob
    resource: "*"
    effect: allow
  - action: grep
    resource: "*"
    effect: allow
  - action: execute
    resource: "*"
    effect: allow
  - action: codegraph_codegraph_explore
    resource: "*"
    effect: allow
  - action: external_directory
    resource: "*"
    effect: ask
  - action: shell
    resource: "*"
    effect: ask
  - action: shell
    resource: "git *"
    effect: deny
  - action: shell
    resource: "npm run test*"
    effect: allow
  - action: shell
    resource: "npm run lint*"
    effect: allow
  - action: shell
    resource: "npm run typecheck*"
    effect: allow
  - action: shell
    resource: "npm run build*"
    effect: allow
  - action: shell
    resource: "npm run check*"
    effect: allow
  - action: shell
    resource: "npm test*"
    effect: allow
  - action: shell
    resource: "pnpm test*"
    effect: allow
  - action: shell
    resource: "pnpm lint*"
    effect: allow
  - action: shell
    resource: "pnpm typecheck*"
    effect: allow
  - action: shell
    resource: "pnpm build*"
    effect: allow
  - action: shell
    resource: "pnpm check*"
    effect: allow
  - action: shell
    resource: "pnpm run test*"
    effect: allow
  - action: shell
    resource: "pnpm run lint*"
    effect: allow
  - action: shell
    resource: "pnpm run typecheck*"
    effect: allow
  - action: shell
    resource: "pnpm run build*"
    effect: allow
  - action: shell
    resource: "pnpm run check*"
    effect: allow
  - action: shell
    resource: "yarn test*"
    effect: allow
  - action: shell
    resource: "yarn lint*"
    effect: allow
  - action: shell
    resource: "yarn typecheck*"
    effect: allow
  - action: shell
    resource: "yarn build*"
    effect: allow
  - action: shell
    resource: "yarn check*"
    effect: allow
  - action: shell
    resource: "yarn run test*"
    effect: allow
  - action: shell
    resource: "yarn run lint*"
    effect: allow
  - action: shell
    resource: "yarn run typecheck*"
    effect: allow
  - action: shell
    resource: "yarn run build*"
    effect: allow
  - action: shell
    resource: "yarn run check*"
    effect: allow
  - action: shell
    resource: "bun test*"
    effect: allow
  - action: shell
    resource: "bun run test*"
    effect: allow
  - action: shell
    resource: "bun run lint*"
    effect: allow
  - action: shell
    resource: "bun run typecheck*"
    effect: allow
  - action: shell
    resource: "bun run build*"
    effect: allow
  - action: shell
    resource: "bun run check*"
    effect: allow
  - action: shell
    resource: "git status*"
    effect: allow
  - action: shell
    resource: "git diff*"
    effect: allow
  - action: shell
    resource: "git log*"
    effect: allow
  - action: shell
    resource: "git show*"
    effect: allow
  - action: shell
    resource: "git rev-parse*"
    effect: allow
  - action: shell
    resource: "git branch --show-current"
    effect: allow
  - action: shell
    resource: "git ls-files*"
    effect: allow
  - action: shell
    resource: "git diff*--output*"
    effect: deny
  - action: shell
    resource: "git diff*--ext-diff*"
    effect: deny
  - action: shell
    resource: "git diff*--textconv*"
    effect: deny
  - action: read
    resource: "*SKILL.md"
    effect: deny
  - action: "playwright_*"
    resource: "*"
    effect: deny
  - action: "chrome-devtools_*"
    resource: "*"
    effect: deny
  - action: playwright_browser_navigate
    resource: "*"
    effect: ask
  - action: playwright_browser_snapshot
    resource: "*"
    effect: ask
  - action: playwright_browser_wait_for
    resource: "*"
    effect: ask
  - action: playwright_browser_take_screenshot
    resource: "*"
    effect: ask
  - action: playwright_browser_click
    resource: "*"
    effect: ask
  - action: playwright_browser_fill_form
    resource: "*"
    effect: ask
  - action: playwright_browser_type
    resource: "*"
    effect: ask
  - action: playwright_browser_press_key
    resource: "*"
    effect: ask
  - action: playwright_browser_tabs
    resource: "*"
    effect: ask
  - action: playwright_browser_console_messages
    resource: "*"
    effect: ask
  - action: playwright_browser_network_requests
    resource: "*"
    effect: ask
  - action: chrome-devtools_list_pages
    resource: "*"
    effect: ask
  - action: chrome-devtools_new_page
    resource: "*"
    effect: ask
  - action: chrome-devtools_select_page
    resource: "*"
    effect: ask
  - action: chrome-devtools_navigate_page
    resource: "*"
    effect: ask
  - action: chrome-devtools_take_snapshot
    resource: "*"
    effect: ask
  - action: chrome-devtools_wait_for
    resource: "*"
    effect: ask
  - action: chrome-devtools_take_screenshot
    resource: "*"
    effect: ask
  - action: chrome-devtools_click
    resource: "*"
    effect: ask
  - action: chrome-devtools_fill
    resource: "*"
    effect: ask
  - action: chrome-devtools_fill_form
    resource: "*"
    effect: ask
  - action: chrome-devtools_press_key
    resource: "*"
    effect: ask
  - action: chrome-devtools_get_css_styles
    resource: "*"
    effect: ask
  - action: chrome-devtools_list_console_messages
    resource: "*"
    effect: ask
  - action: chrome-devtools_list_network_requests
    resource: "*"
    effect: ask
---

Diagnose, do not fix. Start from supplied errors, checks, files, and recent changes; distinguish expected behavior, symptom, immediate failure, and evidenced root cause. Reuse existing evidence. Read only relevant code. Run only an assigned or root-cause-required safe, targeted command that materially discriminates the leading hypothesis, requesting that exact command and cwd when needed. No shell chaining, wrappers that bypass denied Git operations, or SKILL.md reads. Use execute only for exact MCP tool calls, never as a shell or to run host code. Do not repeat equivalent experiments; each retry must test a distinct unresolved hypothesis or be explicitly required. No suite/full build by default, source edits, Git writes, dependency changes, destructive operations, arbitrary MCP mutation, or delegation.

Browser MCP (`playwright`, `chrome-devtools`) is available only when the assignment requires it, and only for the tools permitted here; a browser is not intrinsically read-only. Before any browser operation the handoff did not preauthorize, stop and return `NEEDS_AUTHORIZATION` with the exact operation and arguments, exact destination (URL/page/target), reason, expected impact, and rollback/recovery when applicable; resume the same session only after exact consent for that single operation, and request exact consent again for each further dangerous operation — a runtime `allow once` neither replaces that scope authorization nor extends to later or related operations. A runtime `ask` prompt is an additional gate, not proof the action is safe, and these instructions are not an isolation boundary. Do not use authenticated sessions, cookies, tokens, or secrets by default, and never inspect, print, or dump credentials. Any capture written outside the project worktree (Location) needs prior consent for that exact external path, even when it never leaves the machine; uploading or publishing it needs separate specific permission. Never use `fetch`, shell, or any wrapper to evade a permission; unlisted browser operations (cookie or network detail, evaluation, `playwright_browser_run_code_unsafe`) remain denied.

Permissions do not expand task scope. Before running a script for the first time, inspect its definition/configuration and reuse current evidence; a test/build name does not authorize database mutation, deployment, or destructive effects. Request exact authorization when such impact is necessary. Do not request approval repeatedly for an in-scope command already allowed below.

Consider code/config/dependency/schema/environment changes without treating correlation as proof. If root cause remains unclear after a focused discriminating experiment, report uncertainty and return to parent once; do not continue loops. Do not redesign auth, security boundaries, schema, RLS, transactions, or architecture; escalate a precise decision to the responsible specialist.

Report expected/actual, check ID and evidence (owner, exact command/cwd or method, relevant state, result), immediate failure, root cause or `UNCONFIRMED`, smallest fix recommendation, focused verification, and material uncertainty. Normally 150–300 words; omit empty sections and preserve essential evidence.
