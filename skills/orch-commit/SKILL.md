---
name: orch-commit
description: Orchestrator commit, push, branch, PR, merge and synchronization policy. Load before invoking git-manager for any Git/GitHub mutation.
---

# Orchestrator: commit and Git policy


`auto_commit` and `auto_push` default to `false`; AUTO mode does not imply permission to commit or push.

- AUTO without auto commit: implement automatically, review/verify as required, and leave validated changes uncommitted.
- AUTO with auto commit: invoke `git-manager` only after successful verification (`verifier: PASS` or equivalent deterministic validation explicitly accepted by the workflow). Never automatically commit FAIL, PARTIAL, unresolved validation, or ambiguous working-tree scope.
- GUIDED commits: when specific commit authorization is still missing, present a commit preview (proposed commit message, files intended for inclusion, validation status, known excluded unrelated changes) and wait for explicit approval. If the handoff already carries specific user commit authorization with its scope and validation evidence, do not request the same approval again; invoke `git-manager` under that authorization. Approval of implementation alone is not approval to commit.
- Exceptional authorization: if verification is FAIL or PARTIAL, `git-manager` may run only when the user explicitly requests committing despite that exact validation state.
- Commit authorization workflow: confirm validation status; invoke `git-manager`; let it inspect Git status and the relevant diff; commit only task-related changes; preserve unrelated staged or unstaged work; report commit hash and message.
- Push workflow: confirm the user's explicit push request or `auto_push: true`; invoke `git-manager`; let it inspect branch/remote and outgoing commits; it pushes the requested branch/remote and reports the target. A push-only task needs no new commit and no verification of an existing one, and dirty files not being pushed do not block it.
- Branch/PR/merge/synchronization: invoke `git-manager` only under its own scoped authorization for that family — commit, push, PR, and merge authorizations are distinct and never implied by each other, and branch creation permitted by a scoped task request does not authorize publishing. Serialize these mutations across the repository and worktrees; do not switch, relocate, or remove a branch or worktree while affected writers are active. On conflict, `git-manager` stops and returns the source to its owning implementer; refresh affected validation before further mutation.

- Push safety: never force automatically. Force (`--force*`, `-f`), `--mirror`, `--all`, `--tags`, `--delete`/`-d`, and refspec forms with `:` or a leading `+` require explicit ask-gated authorization, and a non-fast-forward rejection is reported, never resolved by automatic pull/rebase.
