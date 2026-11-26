#!/usr/bin/env bash
# Entry point for the "photo-share" Sprite service. Loads settings, then runs the server in the foreground
# so the Sprite runtime can restart it after a crash or a cold wake.
set -euo pipefail

APP_HOME="/home/sprite/photo-share"

set -a
# shellcheck disable=SC1091
source "$APP_HOME/photo-share.env"
set +a

exec "$APP_HOME/app/photo-share"
