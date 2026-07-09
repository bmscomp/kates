#!/usr/bin/env bash
#
# connect-plugin-loader.sh — Download additional Kafka Connect plugins at runtime.
#
# This script is designed to run as an init container in environments where
# Strimzi's `spec.build` is not available. It downloads plugins from Maven Central
# and extracts them to a shared volume mounted at /plugins.
#
# Usage:
#   Set the EXTRA_PLUGINS environment variable to a comma-separated list of
#   maven artifacts in the format: group:artifact:version
#
# Example:
#   EXTRA_PLUGINS="org.apache.camel.kafkaconnector:camel-aws-s3-sink-kafka-connector:4.8.3,io.aiven:jdbc-connector-for-apache-kafka:6.11.1" ./scripts/connect-plugin-loader.sh

set -euo pipefail

PLUGIN_DIR="/plugins"
MAVEN_REPO="https://repo1.maven.org/maven2"

echo "=========================================================="
echo " Starting Kafka Connect Runtime Plugin Loader"
echo "=========================================================="

if [[ -z "${EXTRA_PLUGINS:-}" ]]; then
  echo "INFO: No EXTRA_PLUGINS environment variable set. Exiting."
  exit 0
fi

# Ensure plugin directory exists
mkdir -p "$PLUGIN_DIR"

# Parse comma-separated list
IFS=',' read -ra PLUGIN_LIST <<< "$EXTRA_PLUGINS"

for plugin_def in "${PLUGIN_LIST[@]}"; do
  # Parse group:artifact:version
  IFS=':' read -r group artifact version <<< "$plugin_def"

  if [[ -z "$group" || -z "$artifact" || -z "$version" ]]; then
    echo "ERROR: Invalid plugin definition: '$plugin_def'. Must be group:artifact:version"
    exit 1
  fi

  echo "----------------------------------------------------------"
  echo "Processing plugin: $artifact ($version)"

  # Convert group (e.g. io.debezium) to path (io/debezium)
  group_path="${group//.//}"
  
  # Determine plugin format. Most Kafka Connect plugins use tar.gz or zip.
  # We will check common formats.
  base_url="${MAVEN_REPO}/${group_path}/${artifact}/${version}"
  tar_url="${base_url}/${artifact}-${version}-plugin.tar.gz"
  zip_url="${base_url}/${artifact}-${version}.zip"
  jar_url="${base_url}/${artifact}-${version}.jar"

  plugin_dest="${PLUGIN_DIR}/${artifact}"
  mkdir -p "$plugin_dest"

  # 1. Try plugin.tar.gz (Debezium standard)
  if curl --output /dev/null --silent --head --fail "$tar_url"; then
    echo "  Downloading: $tar_url"
    curl -sfL "$tar_url" | tar -xz -C "$plugin_dest" --strip-components=1
    echo "  Successfully installed to $plugin_dest"
    continue
  fi

  # 2. Try .zip (Confluent standard)
  if curl --output /dev/null --silent --head --fail "$zip_url"; then
    echo "  Downloading: $zip_url"
    tmp_zip="/tmp/${artifact}.zip"
    curl -sfL -o "$tmp_zip" "$zip_url"
    unzip -q -d "$plugin_dest" "$tmp_zip"
    rm "$tmp_zip"
    echo "  Successfully installed to $plugin_dest"
    continue
  fi

  # 3. Try raw .jar (Simple plugins/converters)
  if curl --output /dev/null --silent --head --fail "$jar_url"; then
    echo "  Downloading: $jar_url"
    curl -sfL -o "${plugin_dest}/${artifact}-${version}.jar" "$jar_url"
    echo "  Successfully installed to $plugin_dest"
    continue
  fi

  echo "ERROR: Could not find a valid tar.gz, zip, or jar for $plugin_def at $base_url"
  exit 1
done

echo "=========================================================="
echo " Plugin loading complete."
echo " Contents of $PLUGIN_DIR:"
ls -la "$PLUGIN_DIR"
echo "=========================================================="
