#!/usr/bin/env bash
# The ClusterRole the operator binds to each Kates pod grants what the Kates
# chart's own ClusterRole grants, with and without direct chaos.
set -euo pipefail
cd "$(dirname "$0")/.."

rules() { # rules <chart> <template> <set> <role name prefix>
  helm template ci "$1" -n kates-operator --show-only "$2" --set "$3" \
    | yq -o=json "select(.kind == \"ClusterRole\" and (.metadata.name | test(\"^$4\"))) | .rules"
}
failed=0
for direct in false true; do
  chart=$(rules charts/kates templates/rbac.yaml "rbac.directChaos=$direct" "ci-kates$")
  operator=$(rules operator/chart templates/backend-rbac.yaml "rbac.backendDirectChaos=$direct" kates-backend-)
  if [ -z "$chart" ] || [ "$chart" != "$operator" ]; then
    echo "direct chaos $direct: the rules differ"
    diff <(echo "$chart") <(echo "$operator") || true
    failed=1
  else
    echo "direct chaos $direct: the rules match"
  fi
done
exit $failed
