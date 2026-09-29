#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Ready-made web/app.js is already supplied. Run npm install && npm run build
# only when changing TypeScript; pin is in package.json.
mkdir -p bin
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${target%/*}; arch=${target#*/}
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -mod=vendor -buildvcs=false -trimpath -ldflags='-s -w' -o "bin/tracework-$os-$arch" .
done
