"""A stand-in binary for the wrapper tests: prints its arguments and the
names of its environment variables as JSON."""

import json
import os
import sys

print(json.dumps({"args": sys.argv[1:], "env_keys": sorted(os.environ)}))
