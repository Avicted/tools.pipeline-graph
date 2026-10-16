#!/usr/bin/env bash
set -euo pipefail
docker run -d --name pg postgres:16
docker run --rm --link pg registry.acme.io/orders:latest migrate
go test -tags integration ./...
