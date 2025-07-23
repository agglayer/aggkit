#!/bin/bash

# Setup script for documentation cache
# This script builds and caches all release versions for faster subsequent builds

set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_status "Setting up documentation cache..."
print_status "This will build all release versions and cache them for future use."
print_status "This process may take several minutes..."

# Run the cache setup
./scripts/build-versioned-docs.sh setup-cache

print_status "Cache setup completed!"
print_status "Future builds will use cached versions for releases and only build the latest version."
print_status "To update a specific release version, run: ./scripts/build-versioned-docs.sh setup-cache"
