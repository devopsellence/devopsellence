---
title: Vibe philosophy
description: Why devopsellence vibe uses a tiny native-web template and stronger agent instructions instead of a full app framework.
---

`devopsellence vibe` is an AI app-building entrypoint, not a framework starter.
Its job is to give an agent a clean workspace, a deployable baseline, and strong
instructions for building a real web app with ordinary web technologies.

The template stays intentionally small. The leverage lives in the skill.

## Native web first

The default stack is:

- Go for the backend.
- SQLite for durable local data.
- Server-rendered HTML.
- Handcrafted CSS.
- Small vanilla JavaScript only when it improves an interaction.
- Docker for the portable build and test path.
- devopsellence for deployment to familiar VMs.

Browsers keep getting more capable. HTML forms, CSS layout, native validation,
`dialog`, `details`, URL APIs, fetch, and storage primitives cover a lot of app
surface without a frontend framework or build chain.

## What vibe avoids

The generated app should not start with:

- React, Vue, Svelte, Next, Astro, or another frontend framework.
- npm, Vite, bundlers, transpilers, or frontend package managers.
- Tailwind, component libraries, CDN UI kits, or generic design systems.
- Client-side routing as the default app model.
- A stack selector.

Those tools can be useful elsewhere. `vibe` chooses not to make them the
starting point because every new tool adds churn, hidden conventions, and more
surface area for an agent to manage.

## Skills over scaffolding

The app template should be understandable in a few minutes. It exists to provide
a safe shape:

- routes;
- templates;
- static assets;
- SQLite setup;
- tests;
- Docker;
- `devopsellence.yml`.

The generated `devopsellence-app` skill carries the harder product guidance:

- derive the first real workflow from the user's idea;
- keep the UI semantic, responsive, and accessible;
- build complete empty, success, validation, and error states;
- use vanilla JavaScript only for progressive enhancement;
- keep every slice deployable;
- run regular subtraction passes so unused scaffolding, stale styles, duplicate
  helpers, placeholder UI, and speculative abstractions do not accumulate;
- checkpoint changes so iteration and revert stay natural.

That split keeps the codebase small while still giving the agent enough taste
and discipline to build a good app.

## Subtraction as quality control

Vibe-generated apps should get clearer as they mature. Every iteration should
ask what can be removed, merged, or simplified before adding another route,
helper, table, style block, or interaction.

Subtraction is not permission to delete confirmed product behavior. It is the
habit of removing leftovers: unused routes, duplicate code, stale tests,
placeholder copy, speculative abstractions, and UI states that no longer serve
the app.

## AI app-builder feel, inspectable output

`vibe` can feel like an AI app builder because the user starts with an idea and
an agent does the work. The difference is the ownership model.

The output is an ordinary repository with ordinary files. A human can inspect the
handlers, templates, CSS, Dockerfile, SQLite schema, and deployment config. The
agent can iterate with git. devopsellence can deploy the result to ordinary
Linux VMs.

The product promise is not magic. It is a narrow, repeatable path from idea to
native-web app to VM deployment.

## Quality bar

A good vibe-generated app should:

- solve one real workflow well;
- load fast without a frontend build artifact;
- work with normal forms before JavaScript enhancement;
- look intentionally designed for its domain;
- expose clear health and error states;
- keep secrets out of prompts, commits, and logs;
- pass `docker build --target test .`;
- remain deployable with `devopsellence deploy --dry-run`.

The goal is durable software, not a throwaway mockup.
