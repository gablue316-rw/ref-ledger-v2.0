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

echo "Current ArgoCD pods:"
kubectl get pods -n argocd

echo "Current application pods:"
kubectl get pods -n default

echo "Starting Ref Ledger port forwarding..."

# Avoid starting a second port-forward process.
if pgrep -f "kubectl port-forward.*service/ref-ledger.*8080:8080" >/dev/null 2>&1; then
    echo "Port forwarding is already running."
else
    nohup kubectl port-forward \
        service/ref-ledger \
        8080:8080 \
        --address=127.0.0.1 \
        > port-forward.log 2>&1 &

    PORT_FORWARD_PID=$!

    sleep 3

    if kill -0 "$PORT_FORWARD_PID" >/dev/null 2>&1; then
        echo "Port forwarding started with PID $PORT_FORWARD_PID"
    else
        echo "Port forwarding failed. Check port-forward.log."
        exit 1
    fi
fi

echo "Ref Ledger is available at http://127.0.0.1:8080"
echo "Cluster started successfully."