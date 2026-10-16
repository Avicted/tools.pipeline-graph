#!/usr/bin/env bash
set -euo pipefail
make -C go lint test
cd kotlin && ./gradlew check
