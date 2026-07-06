#!/bin/bash
set -e

echo "Starting Minikube..."
minikube start

echo "Waiting for Kubernetes node..."
kubectl wait --for=condition=Ready nodes --all --timeout=120s

echo "Checking ArgoCD installation..."
if ! kubectl get namespace argocd >/dev/null 2>&1; then
    echo "ArgoCD namespace not found. Install ArgoCD first."
    exit 1
fi

echo "Waiting for ArgoCD..."
kubectl rollout status deployment/argocd-server -n argocd --timeout=180s
kubectl rollout status deployment/argocd-repo-server -n argocd --timeout=180s
kubectl rollout status statefulset/argocd-application-controller -n argocd --timeout=180s

echo "Waiting for Ref Ledger deployment..."
until kubectl get deployment ref-ledger -n default >/dev/null 2>&1
do
    sleep 5
done

kubectl rollout status deployment/ref-ledger -n default --timeout=180s

echo "Waiting for cloudflared deployment..."
until kubectl get deployment cloudflared -n default >/dev/null 2>&1
do
    sleep 5
done

kubectl rollout status deployment/cloudflared -n default --timeout=180s

echo "Current ArgoCD pods:"
kubectl get pods -n argocd

echo "Current application pods:"
kubectl get pods -n default

echo "Cluster started successfully."