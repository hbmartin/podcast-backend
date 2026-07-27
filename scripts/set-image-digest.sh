#!/usr/bin/env bash
set -euo pipefail

digest="${1:-}"
if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "expected sha256:<64 lowercase hex>" >&2
  exit 2
fi

DIGEST="$digest" perl -0pi -e 's#ghcr\.io/hbmartin/podcast-backend\@sha256:[0-9a-f]{64}#ghcr.io/hbmartin/podcast-backend\@$ENV{DIGEST}#g' render.yaml

# grep exits 1 on zero matches; keep set -e/pipefail intact and let the count
# check below report the failure instead.
count="$(grep -F -o "ghcr.io/hbmartin/podcast-backend@${digest}" render.yaml | wc -l | tr -d ' ')" || true
if [[ "$count" != "4" ]]; then
  echo "expected four Render image pins, found ${count}" >&2
  exit 1
fi
