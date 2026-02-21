#!/bin/sh
set -e

CONFIG=/config/config.yaml

if [ ! -f "$CONFIG" ]; then
    echo "No config.yaml found — copying default to $CONFIG"
    cp /app/config.example.yaml "$CONFIG"
fi

exec /app/nzb-connect --config "$CONFIG"
