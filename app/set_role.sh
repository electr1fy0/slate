#!/bin/bash
set -euo pipefail
ROLE="${1:-active}"
echo "$ROLE" > /etc/continuum/role
systemctl restart continuum
sleep 2
curl -s http://localhost/health/live
