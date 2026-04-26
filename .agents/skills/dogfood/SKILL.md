---
name: dogfood
description: Use when asked to dogfood devopsellence, run manual product QA, test DevX, simulate a new user journey, evaluate product completeness, or produce a dogfood report. The skill guides blind-pass and expert-pass testing with evidence, scenarios, rubrics, and repeatable run artifacts.
---

# Dogfood

## Purpose

Run devopsellence like a real user would. Find bugs, edge cases, product gaps, confusing language, missing docs, and DevX friction.

Dogfood is not only e2e. It asks: can a target user complete the job, understand what happened, recover from failure, and trust the product?

## Core Rules

- Write `devopsellence` lowercase.
- Prefer fresh temp apps and fresh state.
- Start with a blind pass unless the user explicitly asks for code review first.
- During blind pass, use only public/user-facing context: README, docs, CLI help, web UI, generated errors, logs surfaced by the product.
- Do not read implementation source during blind pass.
- After blind pass, run expert pass: inspect source, logs, DB, tests, and root causes.
- Capture evidence: exact commands, key output excerpts, paths, screenshots when UI matters, and time-to-first-success.
- Separate product gaps from bugs. Product completeness and DevX count even when code works.
- Do not hide setup pain. Record confusing prompts, missing next steps, slow feedback, scary output, and cleanup uncertainty.
- Keep secrets and private identifiers out of reports.

## Workflow

1. Pick scenario.
   - If the user names one, use it.
   - Otherwise choose the smallest scenario that answers the request.
   - Read `references/scenarios.md` only when scenario detail is needed.

2. Create run artifact.
   - Prefer `ruby .agents/skills/dogfood/scripts/new_run.rb <scenario>` from repo root; multi-word scenarios may be quoted or passed as multiple words.
   - If the user names a devopsellence version, pass `--version <version>`.
   - If no version is named, omit `--version` and dogfood the default stable installer/control-plane version.
   - Use the temp run path printed by the helper unless the user asks for repo-tracked reports.
   - Keep `commands.log` as you go.

3. Blind pass.
   - Use docs, CLI help, UI, and terminal feedback.
   - Do not inspect source.
   - Install the requested target from `commands.log`: preview versions use `curl -fsSL https://www.devopsellence.com/lfg.sh?version=<version> | bash`; default stable uses `curl -fsSL https://www.devopsellence.com/lfg.sh | bash`.
   - Work from user goals, not privileged steps.
   - Stop only for hard blockers; otherwise recover like a user would.

4. Expert pass.
   - Inspect implementation only after blind evidence is recorded.
   - Root-cause failures and confusing behavior.
   - Check whether existing tests cover the risk.

5. Score and report.
   - Read `references/rubric.md` for scoring when needed.
   - Use `references/report-template.md` for final structure.
   - Lead with outcome, top fixes, and evidence.

6. Optional fixes.
   - If user asks to fix findings, make small reviewable changes.
   - Preserve unrelated worktree changes.
   - Re-run the relevant scenario or narrower verification.

## Scenario Defaults

Use these default personas:

- Solo founder: wants one containerized app live on one VM with ordinary tools.
- Rails developer: understands app code, not infra details.
- Infra-aware skeptic: checks logs, rollback/delete, status, and escape hatches.
- Tired maintainer: bad input, broken deploy, needs clear recovery.

Good default devopsellence journeys:

- Solo first deploy for a fresh Rails app.
- Existing app deploy with secrets.
- Failed deploy diagnosis and recovery.
- Status/log inspection after deploy.
- Delete/cleanup after experiment.
- Shared flow: connect node, deploy app, inspect status.

## Evidence Standard

For each important claim, include one of:

- Command and output excerpt.
- File path and line reference.
- UI screenshot or described visual state.
- Log line with timestamp when available.
- Reproduction steps in user-facing terms.

Do not over-quote logs. Keep enough to prove the point.

## Output Shape

Final response should be short unless user asks for the full report inline:

- run path
- outcome
- top 3 findings
- validation done
- next suggested fix batch

Write the full Markdown report to the run artifact.
