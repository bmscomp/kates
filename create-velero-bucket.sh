#!/bin/bash
set -e

echo "Creating 'velero' bucket in MinIO..."

kubectl run --namespace velero minio-setup \
  --rm -it --restart='Never' \
  --image docker.io/minio/mc:RELEASE.2024-09-16T17-43-14Z \
  --command -- /bin/sh -c "mc alias set local http://minio.velero.svc:9000 minio minio123 && mc mb local/velero || echo 'Bucket might already exist'"

echo "Bucket creation command executed."
