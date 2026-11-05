#!/usr/bin/env bash
# The smallest stack operator/e2e.sh runs on: one-broker Kafka, the Kates API from
# charts/, and this operator with an image the cluster already holds.
#
#   KUBECONFIG=<scratch> operator/e2e-stack.sh <image>
#
# Scratch clusters only: it installs Strimzi and Kafka, as a CI job would.
set -euo pipefail
cd "$(dirname "$0")/.."

image=$1
ns=payments

./scripts/deploy-kafka-generic.sh --yes --skip-tests -f charts/kafka-cluster/values-ci.yaml

# Strimzi writes the SCRAM password in Kafka's namespace, and a pod can only read its own.
kubectl create namespace kates --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kates create secret generic kates-backend --dry-run=client -o yaml \
  --from-literal=password="$(kubectl -n kafka get secret kates-backend -o jsonpath='{.data.password}' | base64 -d)" \
  | kubectl apply -f -

tag=$(sed -nE 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/p' charts/kates/Chart.yaml)
# One broker: Kates' default of three replicas would refuse every topic.
helm upgrade --install kates charts/kates -n kates --wait --timeout 8m \
  --set image.tag="$tag-native" \
  --set-string defaults.replicationFactor=1,defaults.minInsyncReplicas=1 \
  --set-json 'extraEnv=[{"name":"KATES_OUTBOX_TOPIC_REPLICATION_FACTOR","value":"1"}]'

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" create secret generic kates-connection --dry-run=client -o yaml \
  --from-literal=url=http://kates.kates.svc:8080 \
  --from-literal=apiKey="$(kubectl -n kates get secret kates-api-key -o jsonpath='{.data.api-key}' | base64 -d)" \
  | kubectl apply -f -

helm upgrade --install kates-operator operator/chart -n kates-operator --create-namespace --wait --timeout 3m \
  --set image.repository="${image%:*}" --set image.tag="${image##*:}" --set image.pullPolicy=Never \
  --set "watchNamespaces={$ns}" --set 'kafka.namespaces={kafka}'
