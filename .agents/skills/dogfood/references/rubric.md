# Dogfood Rubric

Score each area 1-5 from the AI-agent-mediated perspective. Use plain evidence, not vibes. The question is not “would a human enjoy using this directly?” The question is “can a user safely delegate this devopsellence task to an AI coding/operator agent?”

Terminology: "AI agent" means the AI coding/operator agent doing the dogfood run. "devopsellence node agent" means the runtime reconciler on the VM.

## AI-Agent-Mediated Product Completeness

1. AI agent cannot complete the delegated core goal.
2. Goal possible only with source inspection, privileged knowledge, or unsafe manual hacks.
3. Happy path works, but important lifecycle gaps remain for AI agent operation.
4. Main lifecycle works with understandable recovery and cleanup.
5. Goal, recovery, cleanup, approval boundaries, and user reporting are complete.

## AI Agent DevX

1. Setup or first command blocks the AI agent.
2. Frequent ambiguity; errors lack next steps or require terminal-text guessing.
3. Usable but requires persistence and inference.
4. Mostly clear; friction is localized and reportable.
5. Fast, predictable, structured, and confidence-building for AI agent operation.

## AI Agent Observability

1. AI agent cannot tell what happened.
2. Logs/status exist but are hard to find, parse, or tie to intended state.
3. Basic status works; root cause still takes guessing.
4. Failures point to likely cause and next command.
5. Intended state, observed state, revisions, logs, and recovery are obvious and explainable.

## Docs and Machine Contracts

1. Docs/help mislead or omit essential setup and no machine-readable contract compensates.
2. AI agent must search source or infer the model.
3. Docs/help cover happy path but not recovery or automation.
4. Docs, CLI help, JSON/API shapes, and errors explain model and common failure paths.
5. Docs, CLI help, schemas, structured output, and product language reinforce each other.

## Delegation Trust

1. User would fear data loss, secret leaks, cost leaks, or unknown infrastructure changes from delegating to the AI agent.
2. Product hides important actions, approval boundaries, or cleanup uncertainty.
3. Actions are visible but not fully explainable by the AI agent.
4. Changes, blast radius, approval needs, and cleanup path are clear.
5. Product feels boring, reversible, auditable, and safe to delegate.

## AI-Agent-Primary Operability

1. Workflow requires prompts, browser-only interactions, TTY behavior, or terminal-text scraping.
2. Some commands can be automated, but key actions lack structured output or deterministic errors.
3. Basic non-interactive operation works, but status/errors require inference.
4. Most operations support structured output, meaningful exit codes, and clear next actions.
5. Inspect, validate/plan, deploy, observe, explain, and recover are deterministic and machine-readable.
