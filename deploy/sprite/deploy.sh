#!/usr/bin/env bash
# Deploy the photo wall to a Fly.io Sprite.
#
#   ./deploy/sprite/deploy.sh [sprite-name]
#
# Sprite name: first argument, else $SPRITE_NAME, else the "sprite" field of ./.sprite (set by `sprite use`).
#
# What it does (safe to re-run; photos and the database are never touched):
#   1. builds the Svelte frontend and a static linux/amd64 Go binary
#   2. uploads them to /home/sprite/photo-share/app on the Sprite (atomic swap)
#   3. on the first deploy only, writes /home/sprite/photo-share/photo-share.env with a random
#      DEVICE_SECRET and HOST_PIN (printed once; HOST_PIN can be preset with HOST_PIN=1234 ./deploy.sh)
#   4. creates or restarts the "photo-share" Sprite service on port 8080 (wakes on HTTP requests)
#   5. makes the Sprite URL public and checks /api/health through it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVICE="photo-share"
REMOTE_HOME="/home/sprite/photo-share"

sprite_name="${1:-${SPRITE_NAME:-}}"
if [[ -z "$sprite_name" && -f "$ROOT/.sprite" ]]; then
  sprite_name="$(sed -n 's/.*"sprite"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/.sprite" | head -1)"
fi
if [[ -z "$sprite_name" ]]; then
  echo "No sprite name. Pass one (./deploy/sprite/deploy.sh my-sprite) or run 'sprite use <name>' in $ROOT." >&2
  exit 1
fi

for bin in sprite go npm tar; do
  command -v "$bin" >/dev/null || { echo "Missing required tool: $bin" >&2; exit 1; }
done

sx() { sprite exec -s "$sprite_name" --no-stdin -- "$@"; }
step() { printf '\n==> %s\n' "$*"; }

step "Deploying to sprite '$sprite_name'"

step "Building frontend"
(cd "$ROOT/frontend" && { [[ -d node_modules ]] || npm ci; } && npm run build)

step "Building backend (linux/amd64, static)"
BUILD="$ROOT/build/sprite"
rm -rf "$BUILD" && mkdir -p "$BUILD/app"
(cd "$ROOT/backend" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o "$BUILD/app/photo-share" .)
cp -R "$ROOT/frontend/dist" "$BUILD/app/dist"
cp "$ROOT/deploy/sprite/run.sh" "$BUILD/app/run.sh"
chmod +x "$BUILD/app/run.sh" "$BUILD/app/photo-share"

step "Uploading release"
tar_flags=(-C "$BUILD" -czf -)
if tar --version 2>/dev/null | grep -q bsdtar; then
  tar_flags=(--no-xattrs "${tar_flags[@]}") # keep macOS metadata out of the archive
fi
COPYFILE_DISABLE=1 tar "${tar_flags[@]}" app | sprite exec -s "$sprite_name" --stdin -- bash -c "
  set -euo pipefail
  mkdir -p '$REMOTE_HOME/data/photos' '$REMOTE_HOME/incoming'
  rm -rf '$REMOTE_HOME/incoming/app'
  tar -xzf - -C '$REMOTE_HOME/incoming'
  rm -rf '$REMOTE_HOME/app.previous'
  if [ -d '$REMOTE_HOME/app' ]; then mv '$REMOTE_HOME/app' '$REMOTE_HOME/app.previous'; fi
  mv '$REMOTE_HOME/incoming/app' '$REMOTE_HOME/app'
"

step "Checking environment file"
if sx test -f "$REMOTE_HOME/photo-share.env"; then
  echo "Keeping existing $REMOTE_HOME/photo-share.env"
else
  device_secret="$(openssl rand -hex 32)"
  host_pin="${HOST_PIN:-$(printf '%06d' $((RANDOM * RANDOM % 1000000)))}"
  sed -e "s|__DEVICE_SECRET__|$device_secret|" -e "s|__HOST_PIN__|$host_pin|" \
    "$ROOT/deploy/sprite/photo-share.env.template" |
    sprite exec -s "$sprite_name" --stdin -- bash -c "umask 077 && cat > '$REMOTE_HOME/photo-share.env'"
  echo "Created $REMOTE_HOME/photo-share.env"
  echo "    HOST_PIN for /admin: $host_pin   (shown once; it lives in the env file on the sprite)"
fi

step "Starting service '$SERVICE'"
if sx sprite-env services get "$SERVICE" >/dev/null 2>&1; then
  sx sprite-env services restart "$SERVICE" --duration 3s
else
  sx sprite-env services create "$SERVICE" \
    --cmd "$REMOTE_HOME/app/run.sh" \
    --dir "$REMOTE_HOME" \
    --http-port 8080 \
    --duration 3s
fi

step "Making the URL public"
sprite config update -s "$sprite_name" --url-auth public >/dev/null
url="$(sprite info -s "$sprite_name" | sed -n 's/^URL:[[:space:]]*//p')"

step "Health check"
healthy=""
for attempt in 1 2 3 4 5; do
  if sx bash -c "curl -fsS http://localhost:8080/api/health && echo && curl -fsS '$url/api/health'"; then
    healthy=1
    break
  fi
  echo "  (attempt $attempt failed, retrying)" >&2
  sleep 3
done
if [[ -z "$healthy" ]]; then
  echo "Health check failed. Logs: sprite exec -s $sprite_name -- tail -50 /.sprite/logs/services/$SERVICE.log" >&2
  exit 1
fi

cat <<EOF

Deployed.
  Host panel  : $url/admin   (create an event there to get its room code)
  Join page   : $url/
  Per event   : $url/<room>  (guests)   $url/<room>/wall  (projector)
  Logs        : sprite exec -s $sprite_name -- tail -f /.sprite/logs/services/$SERVICE.log
  Settings    : sprite console -s $sprite_name   then edit $REMOTE_HOME/photo-share.env
Before the event: sprite checkpoint create -s $sprite_name --comment "before event"
EOF
