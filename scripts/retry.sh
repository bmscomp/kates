#!/usr/bin/env bash
# Run a command again when it fails — for the downloads CI cannot make robust
# any other way. `helm dependency build` fetches a subchart from wherever its
# repository lives (kube-prometheus-stack comes from a GitHub release asset)
# and has no retry of its own, so a single 504 from that CDN used to fail a
# job on the step that installs its inputs.
#
# Usage: scripts/retry.sh <attempts> <command...>
#
# The pause grows with the attempt (5s, 10s, 15s, …); the exit status is the
# command's last one.
set -uo pipefail

attempts=$1
shift
[ "${attempts}" -ge 1 ] 2>/dev/null || { echo "retry: attempts must be a positive integer, got '${attempts}'" >&2; exit 2; }

rc=0
for ((i = 1; i <= attempts; i++)); do
    "$@" && exit 0
    rc=$?
    if (( i < attempts )); then
        echo "retry: attempt ${i}/${attempts} of '$*' failed (exit ${rc}), retrying in $((i * 5))s" >&2
        sleep $((i * 5))
    fi
done
echo "retry: '$*' failed ${attempts} times (last exit ${rc})" >&2
exit "${rc}"
