---
title: Docs website component
description: How this docs site is built, checked, and deployed with devopsellence solo.
---

`docs-website/` owns the public devopsellence documentation site. It is a static
Astro Starlight site, separate from the managed control plane.

## Local development

```bash
cd docs-website
npm install
npm run dev
```

## Validation

```bash
cd docs-website
npm run check
npm run build
```

`npm run build` runs `astro check` before `astro build`.

## Solo deployment

This component includes a Dockerfile and `devopsellence.yml`. From
`docs-website/`, initialize and deploy like any other solo app:

```bash
devopsellence init --mode solo
devopsellence node create docs-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519
devopsellence agent install docs-1
devopsellence node attach docs-1
devopsellence deploy --dry-run
devopsellence deploy
```

Set the public hostname before the production deploy:

```bash
devopsellence ingress set --service web --host docs.devopsellence.com --tls-email ops@example.com
devopsellence ingress check --wait 5m
devopsellence deploy
```

The container serves static files with nginx on port `8080`.
