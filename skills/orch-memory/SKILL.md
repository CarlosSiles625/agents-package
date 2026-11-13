---
name: orch-memory
description: Orchestrator Engram session safety and transient execution-state rules. Load before Engram calls needing session/project identity or before persisting/resuming execution state.
---

# Orchestrator: Engram sessions and execution state

## Engram session safety

Never invent, infer, generate, or manually select an Engram session ID. Prefer the authoritative runtime session supplied by OpenCode, then the project identity resolved from `.engram/config.json`. Never choose arbitrarily between multiple runtime sessions. If project or session resolution is ambiguous: do not guess, do not retry across arbitrary sessions, do not create a new identity to bypass ambiguity, skip persistence when memory is not necessary for correctness, and continue the user's task when safe. Memory persistence must never block otherwise valid implementation work. If memory retrieval is necessary for correctness and Engram remains ambiguous, report the ambiguity instead of inventing context.

## Execution state

Durable project memory and temporary execution state are different concepts; durable knowledge belongs to `memory-curator`. The orchestrator may persist minimal transient state only when a multi-stage task may continue across sessions, remembering completed/pending work materially reduces rediscovery, and persistent Engram tools are available with unambiguous session/project identity. Store only a compact plan/status (for example: T1 completed, T2 completed, T3 pending, T4 blocked by T3, Next: T3); never store raw agent transcripts, raw diffs, command output, detailed reasoning, or temporary hypotheses. When resuming: retrieve the compact state, confirm it against current repository evidence, continue from the next unfinished task, and do not rerun completed stages without a concrete reason. Current repository evidence overrides stale execution state. If Engram persistence fails, continue the task normally and report the limitation only when it materially matters.
