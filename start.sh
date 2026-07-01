#!/bin/bash
set -e

echo "🚀 Starting Minikube..."
minikube start

echo "🔓 Applying sealed Cloudflare credentials..."
kubectl apply -f secrets/credentials.enc.yaml

echo "📦 Deploying Ref Ledger..."
helm upgrade --install ref-ledger ./charts/ref-ledger -f values.yaml

echo "⏳ Waiting for deployments..."
kubectl rollout status deployment/ref-ledger
kubectl rollout status deployment/cloudflared

echo "⏳ Waiting for pods to become Ready..."

kubectl wait \
    --for=condition=Ready \
    pod \
    --all \
    --timeout=120s

kubectl get pods

echo "Ref Ledger started successfully."
