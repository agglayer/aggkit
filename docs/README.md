# Aggkit Documentation

This directory contains the documentation for Aggkit, with support for multiple versions.

## Quick Start

### Local Development

1. **First time setup (builds and caches all release versions):**
   ```bash
   make docs-cache-setup
   ```
   This builds all release versions and caches them for future use.

2. **Regular builds (uses cache for releases, builds only latest):**
   ```bash
   make docs-build
   ```
   This builds only the latest version and uses cached versions for releases.

3. **Build and serve locally:**
   ```bash
   make docs-serve
   ```
   This builds the latest version and serves all versions at `http://localhost:8000`

4. **Clean built files and cache:**
   ```bash
   make docs-clean
   ```

### Manual Build

If you prefer to build manually:

```bash
# Build all versions
./scripts/build-versioned-docs.sh

# Test locally
./scripts/test-versioned-docs.sh
```

## Structure

- `SUMMARY.md` - Table of contents for the documentation
- `*.md` - Individual documentation pages
- `assets/` - Static assets (CSS, JS, images)
  - `version-selector.css` - Styling for version selector
  - `version-selector.js` - JavaScript for version switching
- `VERSIONING.md` - Detailed documentation about the versioning system

## Versioning

The documentation supports multiple versions:

- **Latest (develop)** - Current development version
- **v0.5** - Release version 0.5
- **v0.4** - Release version 0.4
- **v0.3** - Release version 0.3
- **v0.2** - Release version 0.2
- **v0.1** - Release version 0.1

Each version is built from its corresponding branch:
- Latest: `develop` branch
- v0.x: `release/0.x` branches

## Adding New Versions

1. Create a new release branch: `release/0.x`
2. Update version configuration in:
   - `docs/assets/version-selector.js`
   - `scripts/build-versioned-docs.sh`
3. Test locally with `make docs-serve`

## Deployment

Documentation is automatically deployed to GitHub Pages when:
- Changes are pushed to the `develop` branch
- The workflow is manually triggered

The deployment process builds all versions and creates a version selector interface.

## Contributing

When updating documentation:

1. **For latest version:** Update files in the `develop` branch
2. **For release versions:** Only make critical fixes (typos, broken links)
3. **Test changes:** Use `make docs-serve` to test locally
4. **Follow structure:** Keep `SUMMARY.md` consistent across versions

## Troubleshooting

### Version Selector Not Working

- Check browser console for JavaScript errors
- Verify `version-selector.js` is included in `book.toml`
- Ensure all version URLs are correct

### Build Failures

- Check that all required branches exist
- Verify each branch has `book.toml` and `docs/` directory
- Check for syntax errors in build scripts

### Local Testing Issues

- Ensure you have Rust and mdBook installed
- Check that all scripts are executable: `chmod +x scripts/*.sh`
- Verify Python is available for local server

For more detailed information, see [VERSIONING.md](./VERSIONING.md).
