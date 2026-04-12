#!/bin/bash
# DEPRECATED: pull-images.sh is no longer needed.
#
# Images are now pulled directly from the registry into Kind cluster nodes
# by load-images-to-kind.sh using 'ctr pull' inside each node's containerd.
#
# Use instead:
#   ./scripts/load-images-to-kind.sh
#
echo "⚠️  pull-images.sh is deprecated."
echo "   Images are loaded directly into Kind from the registry."
echo "   Use: ./scripts/load-images-to-kind.sh"
exit 0
