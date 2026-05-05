# Deploy scripts (prod EC2)

The Pikshipp prod box is a single t3.small in `ap-south-1` running both the
Go backend and the Next.js frontend behind nginx + Let's Encrypt. These
scripts are the one-line redeploy paths from a laptop.

## Prerequisites

- SSH key at `~/.ssh/pikship` (the same key the EC2 instance and your
  GitHub access use).
- Local working tree's `HEAD` pushed to `origin/main` (the frontend script
  refuses to deploy if local and origin diverge).
- For the backend script: a working Go toolchain locally; the binary is
  cross-compiled here and shipped, not built on the box.

## Frontend

```bash
git push                          # push your changes first
./deploy/redeploy-frontend.sh
```

Pulls `origin/main` on the box, runs `npm ci && npm run build`, restarts
`pikshipp-frontend.service`, and curls `https://test.pikall.com/` as a
smoke check.

## Backend

```bash
./deploy/redeploy-backend.sh
```

Cross-compiles `backend/cmd/pikshipp` for `linux/amd64`, ships the binary
plus the latest migrations, runs `migrate up` as the pg superuser, then
restarts `pikshipp-backend.service`. Curls `/healthz` and `/readyz` to
verify.

## Overrides

If the EC2 IP ever changes (instance recreated, EIP reattached elsewhere),
either update the defaults at the top of each script, or override per
invocation:

```bash
PIKSHIP_HOST=ubuntu@1.2.3.4 ./deploy/redeploy-frontend.sh
PIKSHIP_KEY=~/.ssh/other-key ./deploy/redeploy-backend.sh
```
