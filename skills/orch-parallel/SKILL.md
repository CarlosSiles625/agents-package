---
name: orch-parallel
description: Orchestrator task-plan validation, parallel and background execution rules. Load before executing a task-divider TASKS plan or running subagents in parallel/background.
---

# Orchestrator: plan validation and parallelism

## Plan validation

- Validate the returned plan before executing: every `depends_on` ID exists and the graph is an acyclic DAG; `write_scope` and `shared_resources` are consistent across tasks. Do not reject a dependent for writing a predecessor's scope: dependencies are sequential, so a writer may re-acquire a predecessor's scope after that predecessor finished and its deliverable was accepted; only simultaneous ownership of the same scope is a conflict. Return an inconsistent plan to `task-divider`; do not repair it by guessing.

## Parallelism

Parallelism is derived from the plan, not declared as a flag. A task is eligible to start only when every prerequisite it `depends_on` is finished and its result was evaluated as accepted, delivering the required deliverable; the task holds the authorization it needs; its `write_scope` does not overlap an active writer's `write_scope` (an exact path, a listed directory, or a directory/descendant pair is a conflict; a finished predecessor's scope is free to re-acquire); none of its `shared_resources` is currently held by another active task; and, for a task that writes, a writer slot is free.

Resource exclusion applies to every active task, including read-only tasks with `write_scope: []`: before starting, a task acquires and checks its `shared_resources` IDs and releases them when it finishes, so a `shared_resources` ID held by another active task blocks the start regardless of `write_scope`. Never exceed two concurrent implementation writers at any point during execution; read-only tasks do not consume a writer slot. Serialize tasks sharing a `shared_resources` ID (DB/migrations, lockfiles, generated files, build directories) through that single ID. Parallelize independent DAG tasks only after contracts are stable and each writer has exclusive `write_scope` ownership; a fixed shared contract can let its consumers proceed in parallel. Do not stop a whole group when another ready dependent task can proceed; respect dependencies, conflicts, and the concurrency limit, and do not impose rigid waves. Release a writer's ownership only when that executor has finished.

Serialize Git/GitHub mutations across the repository and worktrees through `git-manager`; never switch, relocate, or remove a branch or worktree while affected writers are active.

For independent subagents, request background execution and rely on completion notifications; do not poll, make empty/no-op or redundant tool calls to wait, or promise a native scheduler or measured savings. `/btw` is a user TUI surface, not a tool, approval, or automatic evidence, and guarantees no savings. A background launch returning immediately is not completion: evaluate the actual result before unblocking dependents. A `FAIL`, `BLOCKED`, `ESCALATE`, `NEEDS_AUTHORIZATION`, or incomplete result blocks its descendants, not independent branches, and a global `PARTIAL` does not satisfy a missing prerequisite. Keep a `NEEDS_AUTHORIZATION` task's dependents blocked until it resumes with the granted authorization and reaches `DONE`. Continue the same session ID for a missing piece or correction of the same task when supported. Use worktrees only when their isolation benefit justifies the cost; snapshots are not isolation. Use edge-case review only for real state/boundary/concurrency concerns, and security review only for affected boundaries. Route directly by risk/complexity, not through every level.
