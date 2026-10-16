#!/usr/bin/env bash
set -euo pipefail
make changelog SINCE="$(git describe --tags --abbrev=0)"
