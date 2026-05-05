#!/usr/bin/env bash
# Pull the latest commit on EC2, rebuild the Next.js bundle, restart systemd.
# Override host/key with PIKSHIP_HOST or PIKSHIP_KEY if you ever rebuild the box.
set -euo pipefail

HOST="${PIKSHIP_HOST:-ubuntu@65.1.188.133}"
KEY="${PIKSHIP_KEY:-$HOME/.ssh/pikship}"
SSH=(ssh -i "$KEY" -o StrictHostKeyChecking=no "$HOST")

echo "→ deploying frontend to $HOST"

# Make sure what we're about to deploy is on origin, not just local.
local_head=$(git rev-parse HEAD)
remote_head=$(git ls-remote origin HEAD | awk '{print $1}')
if [[ "$local_head" != "$remote_head" ]]; then
  echo "✗ local HEAD ($local_head) differs from origin HEAD ($remote_head)"
  echo "  push first, then re-run."
  exit 1
fi
echo "  HEAD: $(git log --oneline -1)"

"${SSH[@]}" '
  set -euo pipefail
  cd /opt/pikshipp/repo
  git pull --ff-only
  cd frontend
  npm ci --no-fund --no-audit
  npm run build
  sudo systemctl restart pikshipp-frontend
  sleep 3
  sudo systemctl is-active pikshipp-frontend
'

echo "→ smoke check"
curl -fsS -o /dev/null -w "  https://test.pikall.com/        HTTP %{http_code}\n" https://test.pikall.com/
curl -fsS -o /dev/null -w "  https://test.pikall.com/login   HTTP %{http_code}\n" https://test.pikall.com/login
echo "✓ frontend deployed"
