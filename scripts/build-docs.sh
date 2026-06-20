#!/bin/bash
set -e

# Change to the root of the project
cd "$(dirname "$0")/.."

echo "Generating markdown files from schemas..."
go generate ./schema/...

echo "Building static site..."
if ! command -v mkdocs &> /dev/null; then
    echo "mkdocs not found. Install documentation dependencies first:"
    echo "  pip3 install -r docs/requirements.txt"
    exit 1
fi

mkdocs build

echo "Documentation built successfully in site/ directory."
