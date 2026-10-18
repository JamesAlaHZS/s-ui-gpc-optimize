#!/bin/sh
set -e

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT"

if [ ! -d frontend ]; then
    echo "frontend/ is missing. Restore the tracked frontend source for this fork." >&2
    exit 1
fi

(cd frontend && npm ci && npm run build)

echo "Backend"

mkdir -p web/html
rm -fr web/html/*
cp -R frontend/dist/* web/html/

. "$ROOT/build-tags.sh"
PROFILE="${SUI_BUILD_PROFILE:-dev}"
TAGS=$(tags_for "$PROFILE")
LDFLAGS=$(ldflags_for "$PROFILE")

go build -ldflags "$LDFLAGS" -tags "$TAGS" -o sui main.go
go build -o traffic-review ./cmd/traffic-review
