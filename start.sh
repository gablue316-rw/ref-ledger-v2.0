#!/bin/bash

set -e

echo "🚀 Starting Minikube..."
minikube start

echo "📦 Applying ref-ledger Kubernetes manifests..."
kubectl apply -f k8s/

echo "🔐 Ensuring Cloudflare secret exists..."

if kubectl get secret cloudflared-creds >/dev/null 2>&1; then
  echo "✔ Secret already exists"
else
  echo "Creating cloudflared secret..."
  kubectl create secret generic cloudflared-creds \
    --from-file=credentials.json=secrets/credentials.json
fi

echo "☁️ Applying Cloudflared config..."
kubectl apply -f k8s/cloudflared-config.yaml

echo "☁️ Starting Cloudflared tunnel..."
kubectl apply -f k8s/cloudflared.yaml

echo "🔄 Restarting Cloudflared deployment..."
kubectl rollout restart deployment/cloudflared

echo "⏳ Waiting for pods..."
kubectl get pods -w
