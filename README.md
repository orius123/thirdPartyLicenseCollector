# thirdPartyLicenseCollector

Collect all third party licenses in dependencies into one file, and notify about missing licenses.

## Usage

```bash
go install github.com/aviadl/thirdPartyLicenseCollector@latest

# Vendor mode (default) — reads vendor/modules.txt
thirdPartyLicenseCollector -go-project /path/to/project -format json

# List mode — uses "go list -m" and the module cache (no vendor dir needed)
thirdPartyLicenseCollector -go-project /path/to/project -format json -go-mode list
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-go-project` | | Go project directory |
| `-npm-project` | | npm project directory |
| `-npm-node-modules` | | node_modules directory (if different from npm-project) |
| `-out` | `THIRD_PARTY_LICENSE` | Output file name |
| `-format` | `txt` | Output format: `txt` or `json` |
| `-go-mode` | `vendor` | Go dependency resolution: `vendor` or `list` |

## Go modes

- **`vendor`** (default): Reads `vendor/modules.txt` to discover dependencies and scans `vendor/<module>/` for license files. Requires `go mod vendor` or `go work vendor` to be run first.
- **`list`**: Runs `go list -m -json all` to discover dependencies and reads license files from the Go module cache. No vendor directory needed. Requires modules to be downloaded (`go mod download`).
