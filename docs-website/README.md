# docs-website

`docs-website/` owns the public devopsellence documentation site.

It is an [Astro Starlight](https://starlight.astro.build/) static site,
separate from the managed control plane. The goal is to make docs a product
surface for all devopsellence workflows, not an implementation detail of
`control-plane/`.

## Scope

Use this component for public docs such as:

- getting started guides;
- solo and shared workflow docs;
- CLI and configuration references;
- deployment, ingress, secrets, nodes, and troubleshooting guides;
- migration guides and examples.

## Local development

Use `mise` from this directory:

```sh
mise install
mise run install
mise run dev
```

Or use npm directly:

```sh
npm install
npm run dev
```

## Validation

```sh
mise run check
mise run build
```

`npm run build` runs `astro check` before `astro build`. The static output is
written to `dist/`.

## Deploy with devopsellence solo

This component includes a Dockerfile, nginx config, and `devopsellence.yml`.
From this directory:

```sh
devopsellence init --mode solo
devopsellence node create docs-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install docs-1
devopsellence node attach docs-1
devopsellence deploy --dry-run
devopsellence deploy
```

For production HTTPS, configure ingress before the deploy:

```sh
devopsellence ingress set --service web --host docs.devopsellence.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
devopsellence deploy
```

## Boundaries

`docs-website/` should not own operational endpoints such as installer scripts,
binary downloads, checksums, API routes, auth flows, or node-agent/control-plane
protocols. Those belong to the runtime and product components that serve them.

Repo design notes, architecture records, and implementation specs can remain in
root-level `docs/` unless they are intentionally rewritten for a public docs
audience.
