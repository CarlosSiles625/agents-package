#!/usr/bin/env python3
"""Read-only token telemetry per OpenCode agent, from the local OpenCode database.

Use it to take a baseline before changing prompts, permissions or config, and to
compare afterwards over the same window (`--since`/`--until`).

Metrics per agent (one session = one agent invocation):
  sess        sessions in the window
  steps       assistant messages per session (p50 / p90)
  ctx0        context of the first step (system prompt + tool schemas + handoff), p50
  ctxMax      largest context reached in the session (p50 / p90)
  ctx/step    mean context re-read per step (input + cache read + cache write)
  out/step    mean output tokens per step
  reas/step   mean reasoning tokens per step

The database is opened read-only; nothing is written.
"""

import argparse
import collections
import json
import os
import sqlite3
import sys
import time

DEFAULT_DB = os.path.expanduser("~/.local/share/opencode/opencode.db")


def quantile(values, q):
    values = sorted(values)
    return values[min(len(values) - 1, int(q * len(values)))] if values else 0


def parse_day(value):
    return int(time.mktime(time.strptime(value, "%Y-%m-%d")) * 1000)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default=DEFAULT_DB, help=f"OpenCode database (default: {DEFAULT_DB})")
    ap.add_argument("--days", type=float, help="only the last N days")
    ap.add_argument("--since", help="only from this date (YYYY-MM-DD, local time)")
    ap.add_argument("--until", help="only before this date (YYYY-MM-DD, local time)")
    ap.add_argument("--agent", action="append", help="filter by agent (repeatable)")
    ap.add_argument("--by-model", action="store_true", help="split rows by provider/model#variant")
    ap.add_argument("--min-sessions", type=int, default=1, help="hide rows with fewer sessions (default: 1)")
    ap.add_argument("--json", action="store_true", help="emit JSON instead of a table")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        sys.exit(f"database not found: {args.db}")

    where, params = ["data LIKE '%\"tokens\"%'"], []
    if args.days is not None:
        where.append("time_created >= ?")
        params.append(int((time.time() - args.days * 86400) * 1000))
    if args.since:
        where.append("time_created >= ?")
        params.append(parse_day(args.since))
    if args.until:
        where.append("time_created < ?")
        params.append(parse_day(args.until))

    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    rows = con.execute(
        f"SELECT session_id, seq, data FROM session_message WHERE {' AND '.join(where)}", params
    ).fetchall()
    con.close()

    sessions = collections.defaultdict(list)
    for session_id, seq, data in rows:
        try:
            d = json.loads(data)
        except ValueError:
            continue
        t = d.get("tokens") or {}
        cache = t.get("cache") or {}
        ctx = t.get("input", 0) + cache.get("read", 0) + cache.get("write", 0)
        m = d.get("model") or {}
        model = f"{m.get('providerID')}/{m.get('id')}#{m.get('variant') or 'default'}"
        sessions[session_id].append((seq, d.get("agent") or "?", model, ctx, t.get("output", 0), t.get("reasoning", 0)))

    groups = collections.defaultdict(lambda: {"steps": [], "ctx0": [], "ctxmax": [], "ctx": 0, "out": 0, "reas": 0, "n": 0})
    for msgs in sessions.values():
        msgs.sort()
        agent, model = msgs[0][1], msgs[0][2]
        if args.agent and agent not in args.agent:
            continue
        g = groups[(agent, model) if args.by_model else (agent,)]
        g["steps"].append(len(msgs))
        g["ctx0"].append(msgs[0][3])
        g["ctxmax"].append(max(x[3] for x in msgs))
        for x in msgs:
            g["n"] += 1
            g["ctx"] += x[3]
            g["out"] += x[4]
            g["reas"] += x[5]

    result = []
    for key, g in groups.items():
        if len(g["steps"]) < args.min_sessions:
            continue
        n = g["n"] or 1
        result.append({
            "agent": key[0],
            **({"model": key[1]} if args.by_model else {}),
            "sessions": len(g["steps"]),
            "steps_p50": quantile(g["steps"], 0.5),
            "steps_p90": quantile(g["steps"], 0.9),
            "ctx0_p50": quantile(g["ctx0"], 0.5),
            "ctxmax_p50": quantile(g["ctxmax"], 0.5),
            "ctxmax_p90": quantile(g["ctxmax"], 0.9),
            "ctx_per_step": g["ctx"] // n,
            "out_per_step": g["out"] // n,
            "reasoning_per_step": g["reas"] // n,
            "total_ctx": g["ctx"],
        })
    result.sort(key=lambda r: -r["total_ctx"])

    if args.json:
        json.dump(result, sys.stdout, indent=2)
        print()
        return

    model_w = max([len(r.get("model", "")) for r in result] + [5]) if args.by_model else 0
    head = f"{'agent':22}" + (f" {'model':{model_w}}" if args.by_model else "")
    head += f"{'sess':>6}{'steps50':>8}{'p90':>6}{'ctx0':>8}{'ctxMax50':>10}{'p90':>9}{'ctx/step':>10}{'out/step':>9}{'reas/step':>10}{'total ctx':>12}"
    print(head)
    for r in result:
        line = f"{r['agent']:22}" + (f" {r['model']:{model_w}}" if args.by_model else "")
        line += (f"{r['sessions']:6}{r['steps_p50']:8}{r['steps_p90']:6}{r['ctx0_p50']:8}{r['ctxmax_p50']:10}"
                 f"{r['ctxmax_p90']:9}{r['ctx_per_step']:10}{r['out_per_step']:9}{r['reasoning_per_step']:10}{r['total_ctx']:12,}")
        print(line)
    total = sum(r["total_ctx"] for r in result)
    print(f"\n{len(sessions)} sessions scanned; total context re-read: {total:,} tokens")


if __name__ == "__main__":
    main()
