# Dogfood Rubric

Score each area 1-5. Use plain evidence, not vibes.

## Product Completeness

1. Cannot complete core goal.
2. Goal possible only with privileged knowledge or manual hacks.
3. Happy path works, important lifecycle gaps remain.
4. Main lifecycle works with understandable recovery.
5. Goal, recovery, cleanup, and confidence paths are complete.

## DevX

1. Setup or first command blocks progress.
2. Frequent confusion; errors lack next steps.
3. Usable but requires persistence and inference.
4. Mostly clear; friction is localized.
5. Fast, predictable, and confidence-building.

## Observability

1. User cannot tell what happened.
2. Logs/status exist but are hard to find or interpret.
3. Basic status works; root cause still takes guessing.
4. Failures point to likely cause and next command.
5. State, logs, revisions, and recovery are obvious.

## Docs and Language

1. Docs mislead or omit essential setup.
2. User must search source or infer model.
3. Docs cover happy path but not recovery.
4. Docs explain model and common failure paths.
5. Docs, CLI help, and product language reinforce each other.

## Trust

1. User fears data loss, secret leaks, or unknown infrastructure changes.
2. Product hides important actions.
3. Actions are visible but not fully explainable.
4. Changes and blast radius are clear.
5. Product feels boring, reversible, and inspectable.
