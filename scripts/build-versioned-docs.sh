#!/bin/bash

# Build versioned documentation for Aggkit
# This script builds documentation for the latest version and uses cached versions for releases

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
REPO_URL="https://github.com/agglayer/aggkit"
BUILD_DIR="book"
VERSIONS_DIR="versions"
CACHE_DIR=".docs_cache"
LATEST_VERSION="develop"

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to build docs for latest version only
build_latest_version() {
    local output_dir=$1

    print_status "Building documentation for latest version (develop branch)"

    # Create temporary directory for this version
    local temp_dir=$(mktemp -d)

    # Clone the repository at the develop branch
    git clone --depth 1 --branch "develop" "$REPO_URL" "$temp_dir"

    # Change to the temporary directory
    pushd "$temp_dir" > /dev/null

    # Install mdBook and plugins if not already installed
    if ! command -v mdbook &> /dev/null; then
        print_status "Installing mdBook..."
        curl --proto '=https' --tlsv1.2 https://sh.rustup.rs -sSf -y | sh
        source "$HOME/.cargo/env"
        cargo install mdbook
        cargo install mdbook-mermaid
        cargo install mdbook-alerts
    fi

    # Build the documentation
    print_status "Building mdBook for latest version..."
    mdbook build

    # Copy the built documentation to the output directory
    mkdir -p "$output_dir"
    print_status "Copying latest version to $output_dir"
    if ! cp -rv "$BUILD_DIR"/* "$output_dir/"; then
        print_error "Failed to copy latest version"
        exit 1
    fi

    # Clean up
    popd > /dev/null
    rm -rf "$temp_dir"

    print_status "Successfully built documentation for latest version"
}

# Function to copy cached version
copy_cached_version() {
    local version=$1
    local output_dir=$2
    local cache_path="$CACHE_DIR/$version"

    if [ -d "$cache_path" ]; then
        print_status "Copying cached version $version from cache"
        mkdir -p "$output_dir/$version"
        if ! cp -rv "$cache_path"/* "$output_dir/$version/"; then
            print_error "Failed to copy cached version $version"
            exit 1
        fi
        print_status "Successfully copied cached version $version"
    else
        print_warning "Cached version $version not found, skipping"
    fi
}

# Function to create version selector page
create_version_selector() {
    local output_dir=$1

    cat > "$output_dir/version-selector.html" << 'EOF'
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Aggkit Documentation - Version Selector</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 800px;
            margin: 0 auto;
            padding: 40px 20px;
            line-height: 1.6;
            color: #333;
        }
        .header {
            text-align: center;
            margin-bottom: 40px;
        }
        .version-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 20px;
            margin-top: 30px;
        }
        .version-card {
            border: 1px solid #ddd;
            border-radius: 8px;
            padding: 20px;
            text-align: center;
            transition: transform 0.2s, box-shadow 0.2s;
        }
        .version-card:hover {
            transform: translateY(-2px);
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
        }
        .version-card h3 {
            margin: 0 0 10px 0;
            color: #2c3e50;
        }
        .version-card p {
            margin: 0 0 15px 0;
            color: #666;
        }
        .version-link {
            display: inline-block;
            padding: 10px 20px;
            background: #3498db;
            color: white;
            text-decoration: none;
            border-radius: 4px;
            transition: background 0.2s;
        }
        .version-link:hover {
            background: #2980b9;
        }
        .latest-badge {
            background: #e74c3c;
            color: white;
            padding: 4px 8px;
            border-radius: 12px;
            font-size: 12px;
            margin-left: 8px;
        }
    </style>
</head>
<body>
    <div class="header">
        <h1>Aggkit Documentation</h1>
        <p>Select a version to view the documentation</p>
    </div>

    <div class="version-grid">
        <div class="version-card">
            <h3>Latest (develop) <span class="latest-badge">Latest</span></h3>
            <p>Most recent documentation from the develop branch</p>
            <a href="./" class="version-link">View Documentation</a>
        </div>
        <div class="version-card">
            <h3>v0.5</h3>
            <p>Documentation for version 0.5</p>
            <a href="./v0.5/" class="version-link">View Documentation</a>
        </div>
        <div class="version-card">
            <h3>v0.4</h3>
            <p>Documentation for version 0.4</p>
            <a href="./v0.4/" class="version-link">View Documentation</a>
        </div>
        <div class="version-card">
            <h3>v0.3</h3>
            <p>Documentation for version 0.3</p>
            <a href="./v0.3/" class="version-link">View Documentation</a>
        </div>
        <div class="version-card">
            <h3>v0.2</h3>
            <p>Documentation for version 0.2</p>
            <a href="./v0.2/" class="version-link">View Documentation</a>
        </div>
        <div class="version-card">
            <h3>v0.1</h3>
            <p>Documentation for version 0.1</p>
            <a href="./v0.1/" class="version-link">View Documentation</a>
        </div>
    </div>
</body>
</html>
EOF
}

# Function to build and cache a specific version (for initial setup)
build_and_cache_version() {
    local version=$1
    local branch=$2

    print_status "Building and caching documentation for version $version (branch: $branch)"

    # Create temporary directory for this version
    local temp_dir=$(mktemp -d)

    # Clone the repository at the specific branch
    git clone --depth 1 --branch "$branch" "$REPO_URL" "$temp_dir"

    # Change to the temporary directory
    pushd "$temp_dir" > /dev/null

    # Install mdBook and plugins if not already installed
    if ! command -v mdbook &> /dev/null; then
        print_status "Installing mdBook..."
        curl --proto '=https' --tlsv1.2 https://sh.rustup.rs -sSf -y | sh
        source "$HOME/.cargo/env"
        cargo install mdbook
        cargo install mdbook-mermaid
        cargo install mdbook-alerts
    fi

    # Build the documentation
    print_status "Building mdBook for version $version..."
    mdbook build

    # Cache the built documentation
    mkdir -p "$CACHE_DIR/$version"
    print_status "Caching version $version to $CACHE_DIR/$version"
    if ! cp -rv "$BUILD_DIR"/* "$CACHE_DIR/$version/"; then
        print_error "Failed to cache version $version"
        exit 1
    fi

    # Clean up
    popd > /dev/null
    rm -rf "$temp_dir"

    print_status "Successfully built and cached documentation for version $version"
}

# Main execution
main() {
    print_status "Starting versioned documentation build (cached mode)..."

    # Create output directory
    mkdir -p "$VERSIONS_DIR"

    # Build latest version (develop branch)
    build_latest_version "$VERSIONS_DIR"

    # Copy cached versions for releases
    copy_cached_version "v0.5" "$VERSIONS_DIR"
    copy_cached_version "v0.4" "$VERSIONS_DIR"
    copy_cached_version "v0.3" "$VERSIONS_DIR"
    copy_cached_version "v0.2" "$VERSIONS_DIR"
    copy_cached_version "v0.1" "$VERSIONS_DIR"

    # Create version selector page
    print_status "Creating version selector page..."
    create_version_selector "$VERSIONS_DIR"

    print_status "Versioned documentation build completed!"
    print_status "Output directory: $VERSIONS_DIR"
    print_status "Cache directory: $CACHE_DIR"
}

# Function to build all versions for initial cache setup
setup_cache() {
    print_status "Setting up documentation cache (this may take a while)..."

    # Create cache directory
    mkdir -p "$CACHE_DIR"

    # Build and cache all versions
    build_and_cache_version "v0.5" "release/0.5"
    build_and_cache_version "v0.4" "release/0.4"
    build_and_cache_version "v0.3" "release/0.3"
    build_and_cache_version "v0.2" "release/0.2"
    build_and_cache_version "v0.1" "release/0.1"

    print_status "Cache setup completed!"
    print_status "Cache directory: $CACHE_DIR"
}

# Check command line arguments
if [ "$1" = "setup-cache" ]; then
    setup_cache
else
    main "$@"
fi
