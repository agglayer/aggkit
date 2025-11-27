# AGENTS.md

## Build/Test Commands

- `make build` - Build all binaries
- `make lint` - Run golangci-lint (timeout set to 5m via command-line argument in the Makefile)
- `make test-unit` - Run all unit tests with coverage
- `go test -v -run TestName ./path/to/package` - Run a single test
- `go test -v -run TestName ./...` - Run single test across all packages

## Code Style

- **Formatting**: gofmt, goimports, gci (auto-enforced by linter)
- **Imports**: Group stdlib, then external, then internal (gci enforced). Use aliases for conflicts: `aggkitcommon` (`github.com/agglayer/aggkit/common`), `aggkittypes` (`github.com/agglayer/aggkit/types`), `agglayertypes` (`github.com/agglayer/aggkit/agglayer/types`)
- **Naming**: CamelCase for exports, camelCase for private. Avoid stuttering (e.g., `config.Config` not `config.ConfigStruct`)
- **Errors**: Use `fmt.Errorf("context: %w", err)` for wrapping. Check errors immediately after calls
- **Types**: Prefer interfaces for dependencies. Define mocks in `mocks/` subdirectories
- **Line length**: 120 chars max (lll linter)
- **Constants**: Use typed constants, group related ones in const blocks
- **Comments**: Exported functions require doc comments starting with function name

## Project Structure

- Main components: aggoracle, aggsender, bridgeservice, l1infotreesync, bridgesync, l2gersync, reorgdetector, etherman, sync, multidownloader
- Each component has: config, types, mocks subdirectories
- Tests use testify; files named `*_test.go`. Use `require` (not `assert`) when creating unit tests
- Database migrations in `migrations/` subdirectories
