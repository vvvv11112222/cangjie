#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
export PYTHONDONTWRITEBYTECODE=1
integration_python="${PYTHON_EXECUTABLE:-python3}"
"$integration_python" -m unittest discover -s integration -p 'test_*.py' -v
"$integration_python" -m unittest discover -s integration/upstream/tools -p test_quality.py -v
