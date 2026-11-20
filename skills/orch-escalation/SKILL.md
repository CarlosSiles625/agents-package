---
name: orch-escalation
description: Orchestrator model-escalation policy (Opus and DeepSeek max overrides). Load before applying any subagent model override.
---

# Orchestrator: model escalation


Profile defaults govern routing; never escalate a model by default. Model load is balanced across three subscriptions: OpenAI carries continuous work (`orchestrator`, `debugger`, `implementer-deep`), Claude carries low-frequency judgment (`architect` on Opus; `reviewer`, `security-reviewer`, `database-specialist` on Sonnet; `debugger-deep` on Opus), execution (`database-implementer` on Sonnet `#high`; `edge-case-reviewer`, `implementer`, `task-divider`, `debugger-fast`, and `general-purpose` on Sonnet `#medium`), and low-cost scoped work (`memory-curator`, `researcher`, `repo-explorer`, and `git-manager` on Haiku `#low`), while OpenCode Go carries other mechanical, short-step work. Use the native subagent `model` argument on the SAME profile (no new profile, no invented frontmatter field); if the override is unsupported, run the profile default and say so rather than silently degrading. Note every applied override model/variant and its trigger in the handoff.

Opus (`claude-agent/opus`) is a scarce, quota-limited resource. Beyond the `architect` default and `debugger-deep`, escalate to it only on these triggers:

- `security-reviewer` → `model: claude-agent/opus#high` only when the change materially touches authentication, authorization, cryptography, payments, tenant isolation, privileged execution, or secrets handling. Other security reviews stay on the Sonnet default.
- `database-specialist` → `model: claude-agent/opus#medium` only for irreversible migrations, schema changes on live production data, or public schema-contract changes.
- Never escalate `orchestrator`, `reviewer`, any implementer, or any OpenCode Go profile to Opus; for hard implementation, have `debugger-deep` or `architect` produce the plan and let the implementer execute it.

DeepSeek max: use `model: opencode-go/deepseek-v4.1-flash#max` only on the Deep-lane `implementer-deep` and only when the user explicitly requests/authorizes that model for the task: a difficult but bounded problem with design settled and verifiable acceptance, or a `high` failure clearly caused by reasoning, with evidence and room to go. No default `max`, no mandatory escalation chain, no use for missing context/documentation; broad/high-risk decisions belong to `architect`/`security-reviewer`, and the DB executor stays on its profile until the migration pilot.

Respect retry policy and budget; claim no savings/quota without telemetry.
