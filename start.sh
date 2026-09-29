#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
case "$(uname -s)" in Linux) system=linux;; Darwin) system=darwin;; *) echo 'Use Linux or macOS; see README.md.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) machine=amd64;; aarch64|arm64) machine=arm64;; *) echo 'Unsupported architecture.' >&2; exit 1;; esac
binary="./bin/tracework-$system-$machine"
if [ ! -x "$binary" ]; then chmod +x "$binary"; fi
if [ "$#" -eq 0 ]; then set -- serve; fi
exec "$binary" "$@"
