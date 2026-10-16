#!/usr/bin/env bash
set -euo pipefail
ENV="$1"
make -C . rollout ENV="$ENV"
