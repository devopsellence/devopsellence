# Shared release core

P4 should converge solo and shared on one logical release/deployment model.
Shared Rails already has the better shape: immutable releases, deployments as
operations, environment current-release pointers, and per-node desired-state
publication. Solo should use that same model with a local store.

## Core model

`deployment-core/pkg/deploycore/release` owns:

- environments;
- nodes;
- immutable releases;
- deployments;
- desired-state publications;
- rollback selection;
- desired-state publication planning;
- a store interface for mode-specific persistence and publication IO.

The store is the mode boundary:

- solo store: local state plus SSH/file desired-state publication;
- shared store: Rails/Postgres plus object storage or standalone desired-state
  document publication.

The core must not know whether a deployment is solo or shared.

## Rollback

Rollback is a deployment operation pointing at an old release. It should not
create a new release and should not rebuild an image. By default it republishes
the previous desired-state snapshot and waits for node reconciliation.

Selector rules:

- empty selector means the previous release before the current release;
- explicit selector matches release id, exact revision, or unique revision
  prefix;
- ambiguous selectors fail with an error; selectors that match no release also
  fail with an error. These are currently plain error strings, not structured
  error values or types.

## Current state

Solo deploy now records core release and deployment records, publishes desired
state through the core publication planner, and exposes:

```sh
devopsellence release list
devopsellence release rollback [revision-or-release-id]
```

The next step is replacing Rails' Ruby desired-state publisher with the same
core service through an RPC or binary boundary backed by a Rails store adapter.
