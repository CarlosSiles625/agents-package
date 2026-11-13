---
name: orch-modes
description: Orchestrator GUIDED and AUTO execution-mode procedures (Stage Preview/Result format, AUTO pause triggers). Load before the first stage of a task.
---

# Orchestrator: execution modes

## GUIDED mode

Use when the user wants to inspect, approve, adjust, skip, or stop meaningful stages. Before each meaningful stage, present a Stage Preview (stage number/name; specialist — an agent, or direct coordination with no implementation or commands; objective; why needed; scope; expected output; validation), then stop and wait for explicit approval. Accept approve, continue, modify, skip, stop, change scope, or `mode: auto`, and do not execute the stage before approval. After completing an approved stage return a Stage Result (status COMPLETE/BLOCKED/FAILED/PARTIAL, concise result, evidence, next proposed stage), then present the next Stage Preview and wait again. Do not request approval for trivial internal reasoning that invokes no specialists, runs no commands, uses no external tools, and modifies no state, and do not create approval fatigue by splitting a meaningful stage into trivial sub-stages.

## AUTO mode

Use when the user requests autonomous or end-to-end execution. At the beginning provide one concise execution plan (for example: locate affected code → `repo-explorer`; implement via selected executor; verify pending acceptance criteria → `verifier`), then execute the required workflow without asking for approval between normal stages. Continue automatically, when justified, through memory retrieval, repository discovery, external research, architecture, database/security analysis, task decomposition, implementation, review, verification, durable memory updates, execution-state updates, and commit creation when `auto_commit: true` and verification returned PASS, and push when the user explicitly requested it or `auto_push: true`. Do not pause merely to report progress. Pause only when explicit approval is required by safety policy or tool permissions, a destructive operation is required, a write targets a path outside the active Location/project worktree, a required decision cannot safely be inferred, scope materially changes, credentials/secrets/user-provided input are required, an unresolved blocker prevents safe continuation, or committing would include ambiguous or unrelated changes. AUTO never overrides safety rules or tool permissions. At completion, provide one concise final report.
