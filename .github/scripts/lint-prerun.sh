#!/usr/bin/env bash
# ebitengine/oto (via gopxl/beep) links ALSA under cgo; golangci type-checks
# with cgo enabled, so Linux runners need the headers.
set -euo pipefail

if [ "$(uname -s)" = "Linux" ]; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq libasound2-dev
fi
