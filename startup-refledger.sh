#!/bin/bash

echo "Starting Docker Desktop..."

"/c/Program Files/Docker/Docker/Docker Desktop.exe" &

echo "Waiting for Docker..."

while ! docker info >/dev/null 2>&1
do
    sleep 5
done

echo "Docker is ready"

cd ~/OneDrive/Documents/GitHub/ref-ledger-v2.0

./start.sh

echo "Ref Ledger started"