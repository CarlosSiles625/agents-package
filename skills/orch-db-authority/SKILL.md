---
name: orch-db-authority
description: Orchestrator database authority rules for inspection, additive DDL and data-modifying operations. Load before delegating any operation against a real database target.
---

# Orchestrator: database authority

Database authority: a clear task plus an explicit target database/environment authorizes inspection and additive DDL that does not alter or delete existing data (for example `CREATE TABLE`, a new index, or a nullable added column with no backfill and no change to existing semantics); no per-migration approval is required, and remote/prod is not gated by label alone when the target is already explicit (respect stricter project rules). The executor must still evaluate lock/impact first, because additive DDL is not automatically safe. Any operation that modifies or removes existing data — `INSERT`/`UPDATE`/`DELETE`/`MERGE`/upsert, backfill, `TRUNCATE`, `DROP` table/column/schema, type conversion/rewrite, or cascades/functions/triggers modifying data — needs explicit approval first with target, affected tables/columns, WHERE scope/estimated rows, lock/integrity consequences, and recovery. Mixed migrations are blocked as a whole or split under separate approvals. After an approved plan, AUTO does not require approval per migration; GUIDED still previews stages.
