# Documentation Versioning

This document explains how the versioned documentation system works for Aggkit.

## Overview

The documentation system supports multiple versions, allowing users to view documentation for different releases. Each release branch (e.g., `release/0.1`, `release/0.2`) contains its own documentation that gets built and deployed to GitHub Pages.

## Structure

```
GitHub Pages Structure:
/
├── index.html          # Version selector page
├── getting_started.html # Latest docs (develop branch)
├── aggsender.html
├── ...
├── v0.5/              # Version 0.5 docs
│   ├── index.html
│   ├── getting_started.html
│   └── ...
├── v0.4/              # Version 0.4 docs
│   ├── index.html
│   ├── getting_started.html
│   └── ...
└── ...
```

## How It Works

### 1. Version Selector

- A version selector appears in the top-right corner of all documentation pages
- Users can switch between different versions using the dropdown
- The selector automatically detects the current version from the URL path

### 2. Build Process

The GitHub Actions workflow (`mdbook.yml`) automatically:

1. **Builds only the latest version** (develop branch) - this is the only version that changes frequently
2. **Uses cached versions** for all release branches (v0.1, v0.2, v0.3, v0.4, v0.5) - these are pre-built and cached
3. Creates a version selector page
4. Deploys everything to GitHub Pages

**Benefits of this approach:**
- **Faster builds**: Only one version is built per deployment
- **Reduced resource usage**: No need to clone and build all release branches
- **Consistent release versions**: Release documentation remains stable
- **Easy updates**: Only the latest version needs to be updated

### 3. Version Management

#### Adding a New Version

1. Create a new release branch: `release/0.x`
2. Update the version configuration in:
   - `docs/assets/version-selector.js` (add new version to the `versions` object)
   - `scripts/build-versioned-docs.sh` (add new version to `version_branches` array)
   - Update the version index page in the build script

#### Example: Adding v0.6

```javascript
// In docs/assets/version-selector.js
const versions = {
    'latest': { name: 'Latest (develop)', url: '/' },
    'v0.6': { name: 'v0.6', url: '/v0.6/' },
    'v0.5': { name: 'v0.5', url: '/v0.5/' },
    // ... other versions
};
```

```bash
# In scripts/build-versioned-docs.sh
declare -A version_branches=(
    ["latest"]="develop"
    ["v0.6"]="release/0.6"
    ["v0.5"]="release/0.5"
    # ... other versions
)
```

### 4. Local Development

To test the versioned documentation locally:

```bash
# First time setup (builds and caches all release versions)
make docs-cache-setup

# Regular builds (uses cache for releases, builds only latest)
make docs-build

# Build and serve locally
make docs-serve
```

Then visit `http://localhost:8000` to see the version selector.

**Note**: The first time you run `make docs-cache-setup`, it will take longer as it builds all release versions. Subsequent builds will be much faster as they use the cached versions.

## File Structure

### Core Files

- `book.toml` - mdBook configuration with version selector support
- `docs/assets/version-selector.css` - Styling for the version selector
- `docs/assets/version-selector.js` - JavaScript for version switching
- `scripts/build-versioned-docs.sh` - Build script (uses cache for releases)
- `scripts/setup-docs-cache.sh` - Cache setup script
- `.github/workflows/mdbook.yml` - GitHub Actions workflow
- `.docs_cache/` - Cached release versions (created after first setup)

### Version-Specific Files

Each release branch should contain:
- `docs/` - Documentation source files
- `book.toml` - mdBook configuration (can be version-specific)

## Best Practices

### 1. Documentation Updates

- Always update documentation in the `develop` branch for the latest version
- For release branches, only make critical fixes (typos, broken links)
- Major documentation changes should go to `develop` only

### 2. Version Compatibility

- Ensure all versions have the same basic structure
- Keep the SUMMARY.md file consistent across versions
- Test that the version selector works correctly

### 3. Testing

Before deploying:

1. Build all versions locally: `./scripts/build-versioned-docs.sh`
2. Test navigation between versions
3. Verify that all links work correctly
4. Check that the version selector appears on all pages

### 4. Troubleshooting

#### Version Selector Not Appearing

- Check that `version-selector.js` is included in `book.toml`
- Verify the JavaScript file is being loaded (check browser console)
- Ensure the CSS file is also included

#### Broken Links Between Versions

- Check that the URL structure is consistent
- Verify that all referenced pages exist in each version
- Test navigation manually

#### Build Failures

- Check that all required branches exist
- Verify that each branch has the necessary files (`book.toml`, `docs/`)
- Check for syntax errors in the build script

## Configuration

### Customizing the Version Selector

Edit `docs/assets/version-selector.js` to:
- Add/remove versions
- Change version names
- Modify the URL structure
- Add custom styling

### Styling

Edit `docs/assets/version-selector.css` to:
- Change the appearance of the selector
- Modify positioning
- Add responsive design
- Support dark themes

## Deployment

The versioned documentation is automatically deployed when:
- Changes are pushed to the `develop` branch
- The workflow is manually triggered

The deployment process:
1. Builds documentation for all configured versions
2. Creates a version index page
3. Uploads everything to GitHub Pages
4. Makes the documentation available at your GitHub Pages URL

## Support

For issues with the versioning system:
1. Check the GitHub Actions logs for build errors
2. Test locally using the build script
3. Verify that all required files are present
4. Check browser console for JavaScript errors
