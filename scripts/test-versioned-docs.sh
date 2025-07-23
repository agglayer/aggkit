#!/bin/bash

# Test script for versioned documentation
# This script builds the documentation and serves it locally for testing

set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Check if we're in the right directory
if [ ! -f "book.toml" ]; then
    echo "Error: book.toml not found. Please run this script from the project root."
    exit 1
fi

print_status "Building versioned documentation..."

# Build all versions
./scripts/build-versioned-docs.sh

if [ $? -eq 0 ]; then
    print_status "Build completed successfully!"
    print_status "Starting local server..."
    print_status "Visit http://localhost:8000 to view the documentation"
    print_status "Press Ctrl+C to stop the server"

    # Start local server
    cd versions
    python3 -m http.server 8000
else
    print_error "Build failed. Please check the error messages above."
    exit 1
fi
