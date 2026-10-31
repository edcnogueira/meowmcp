#!/usr/bin/env bash
# Installs or updates meowmcp on the homelab: syncs compose.yaml, keeps the server-side .env,
# pulls the image published by CI and restarts the container. The WhatsApp session lives in a
# named volume and survives updates.
#
# Usage: deploy/deploy.sh [tag]    tag: main (default), sha-<7 chars>, or a release like 0.2.0
# Env:   MEOWMCP_DEPLOY_HOST (default edchomelab.local), MEOWMCP_DEPLOY_DIR (default services/meowmcp, relative to the remote $HOME)
set -euo pipefail

host="${MEOWMCP_DEPLOY_HOST:-edchomelab.local}"
dir="${MEOWMCP_DEPLOY_DIR:-services/meowmcp}"
tag="${1:-main}"
here="$(cd "$(dirname "$0")" && pwd)"

ssh "$host" "mkdir -p ~/$dir ~/.hermes/plugins/meowmcp-approval"
scp -q "$here/compose.yaml" "$host:$dir/compose.yaml"
scp -q "$here/../integrations/hermes/meowmcp-approval/plugin.yaml" "$here/../integrations/hermes/meowmcp-approval/__init__.py" \
  "$host:.hermes/plugins/meowmcp-approval/"

ssh "$host" bash -s -- "$dir" "$tag" <<'REMOTE'
set -euo pipefail
cd "$HOME/$1"
tag="$2"
umask 077
touch .env
chmod 600 .env

for key in MEOWMCP_AUTH_TOKEN MEOWMCP_SEND_APPROVAL_SECRET; do
  if ! grep -q "^$key=." .env; then
    sed -i "/^$key=/d" .env
    echo "$key=$(openssl rand -hex 32)" >> .env
    echo "[deploy] generated $key in $PWD/.env"
  fi
done
mkdir -p data
sed -i '/^MEOWMCP_\(TAG\|DATA_DIR\|UID\|GID\)=/d' .env
cat >> .env <<EOF
MEOWMCP_TAG=$tag
MEOWMCP_DATA_DIR=$PWD/data
MEOWMCP_UID=$(id -u)
MEOWMCP_GID=$(id -g)
EOF

# Hermes reads ${MEOWMCP_AUTH_TOKEN} for the MCP connection and MEOWMCP_SEND_APPROVAL_SECRET in the
# meowmcp-approval plugin from its own .env; keep both in sync.
hermes_env="$HOME/.hermes/.env"
if [[ -f "$hermes_env" ]]; then
  for key in MEOWMCP_AUTH_TOKEN MEOWMCP_SEND_APPROVAL_SECRET; do
    value="$(sed -n "s/^$key=//p" .env)"
    if ! grep -qx "$key=$value" "$hermes_env"; then
      sed -i "/^$key=/d" "$hermes_env"
      echo "$key=$value" >> "$hermes_env"
      echo "[deploy] wrote $key to $hermes_env; restart Hermes to load it: systemctl --user restart hermes-gateway"
    fi
  done
fi

echo "[deploy] ghcr.io/edcnogueira/meowmcp:$tag"
docker compose pull --quiet
docker compose up -d --remove-orphans

sleep 3
if [[ -z "$(docker compose ps --status running -q meowmcp)" ]]; then
  echo "[deploy] error: container is not running"
  docker compose logs --tail 40 meowmcp
  exit 1
fi
docker compose logs --tail 5 meowmcp
docker image prune -f --filter "label=org.opencontainers.image.source=https://github.com/edcnogueira/meowmcp" >/dev/null
echo "[deploy] up: http://127.0.0.1:$(sed -n 's/^MEOWMCP_PORT=//p' .env | grep . || echo 8484)/mcp"
REMOTE
