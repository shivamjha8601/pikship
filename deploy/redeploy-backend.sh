#!/usr/bin/env bash
# Cross-compile backend, ship the binary + migrations to EC2, restart systemd.
# We compile locally and scp because the t3.small doesn't have go installed
# (and `go build` on a 2GB box is wasteful when it's a 1-second job on a laptop).
set -euo pipefail

HOST="${PIKSHIP_HOST:-ubuntu@65.1.188.133}"
KEY="${PIKSHIP_KEY:-$HOME/.ssh/pikship}"
SSH=(ssh -i "$KEY" -o StrictHostKeyChecking=no "$HOST")
SCP=(scp -i "$KEY" -o StrictHostKeyChecking=no)

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN=/tmp/pikshipp-linux

echo "→ cross-compiling backend (linux/amd64)"
cd "$REPO_ROOT/backend"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$BIN" ./cmd/pikshipp
ls -lh "$BIN" | awk '{print "  binary: "$5}'

echo "→ shipping to $HOST"
"${SCP[@]}" "$BIN" "$HOST:/tmp/pikshipp"

# Migrations only need re-shipping if any new ones were added; cheap enough to
# always sync so we don't forget.
"${SCP[@]}" -r "$REPO_ROOT/backend/migrations" "$HOST:/tmp/migrations-new"

"${SSH[@]}" '
  set -euo pipefail
  sudo install -m 755 /tmp/pikshipp /opt/pikshipp/pikshipp
  sudo rsync -a --delete /tmp/migrations-new/ /opt/pikshipp/migrations/
  rm -rf /tmp/migrations-new

  # Apply any new migrations as the pg superuser before restarting the binary;
  # the binary refuses to serve on out-of-date schema.
  SUPERUSER_URL="postgres://postgres@/pikshipp?host=/var/run/postgresql&sslmode=disable"
  sudo -u postgres migrate -path /opt/pikshipp/migrations -database "$SUPERUSER_URL" up

  sudo systemctl restart pikshipp-backend
  sleep 3
  sudo systemctl is-active pikshipp-backend
'

echo "→ smoke check"
curl -fsS -o /dev/null -w "  https://be.pikall.com/healthz   HTTP %{http_code}\n" https://be.pikall.com/healthz
curl -fsS -o /dev/null -w "  https://be.pikall.com/readyz    HTTP %{http_code}\n" https://be.pikall.com/readyz
echo "✓ backend deployed"
