#!/bin/bash
echo "=== Debugging Kates to Kafka Connection ==="
echo "[1] Checking kates namespace labels..."
kubectl get ns kates --show-labels

echo "[2] Checking kafka namespace labels..."
kubectl get ns kafka --show-labels

echo "[3] Checking kates-backend secret in kates namespace..."
kubectl get secret kates-backend -n kates -o jsonpath="{.data.password}" | base64 -d && echo ""

echo "[4] Checking kates-backend secret in kafka namespace..."
kubectl get secret kates-backend -n kafka -o jsonpath="{.data.password}" | base64 -d && echo ""

echo "[5] Testing network connectivity from kates pod..."
POD=$(kubectl get pod -l app.kubernetes.io/name=kates -n kates -o jsonpath="{.items[0].metadata.name}")
kubectl exec -it $POD -n kates -- nc -vz krafter-kafka-bootstrap.kafka.svc 9092
