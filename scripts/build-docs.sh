#!/bin/bash
set -e

# Change to the root of the project
cd "$(dirname "$0")/.."

echo "Generating markdown files from schemas..."
go generate ./schema/...

echo "Building static site..."
if ! command -v mkdocs &> /dev/null; then
    echo "mkdocs not found. Installing mkdocs and mkdocs-material..."
    pip3 install mkdocs mkdocs-material
fi

mkdocs build

echo "Documentation built successfully in site/ directory."
