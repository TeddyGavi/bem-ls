# Releasing

Releases are cut by pushing a version tag; GitHub Actions does the rest
(`.github/workflows/release.yml`).

```sh
make && make fuzz            # last local check
make snapshot                # optional: dry-run the release build into dist/
git tag v0.1.0
git push origin v0.1.0
```

The workflow then:

1. runs the tests and **goreleaser** (`.goreleaser.yaml`): static binaries
   (`CGO_ENABLED=0`) for darwin, linux and windows on amd64 and arm64,
   archived as `bem-ls_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) and
   `jsx2scss_…`, with `checksums.txt`, on a new GitHub release;
2. packages **one VSIX per platform** (`darwin-arm64`, `darwin-x64`,
   `linux-x64`, `linux-arm64`, `alpine-x64`, `alpine-arm64`, `win32-x64`,
   `win32-arm64`), each with its own `bem-ls` binary, and attaches them to the
   release; the extension version is taken from the tag;
3. publishes the VSIX files to the **VS Code Marketplace** and **Open VSX**.

Tags with a suffix (`v0.2.0-rc.1`) make a GitHub pre-release and skip
step 3.

## Secrets

Set these in the repository settings (Settings → Secrets and variables →
Actions). Without them, step 3 is skipped with a notice.

| Secret | Where to get it |
|---|---|
| `AZURE_TENANT_ID` | An Azure Tenant configured via entra Id and with the *Marketplace: Manage* scope, for the publisher in `editors/vscode/package.json` (`teddygavi`) |
| `AZURE_CLIENT_ID` | An Azure Client with federated credential id with the *Marketplace: Manage* scope, for the publisher in `editors/vscode/package.json` (`teddygavi`) |
| `AZURE_SUBSCRIPTION_ID` | An Azure sub scriptions and matching entra id with the *Marketplace: Manage* scope, for the publisher in `editors/vscode/package.json` (`teddygavi`) |
| `OVSX_PAT` | An access token from open-vsx.org, for the same namespace |

## Local packaging

```sh
make vsix TARGET=darwin-arm64     # or TARGET=all; files land in editors/vscode/vsix/
```

## CI

`.github/workflows/ci.yml` runs on pushes and pull requests: `make lint`
(gofmt, go vet, staticcheck, `go mod tidy -diff`), race tests on Linux and
macOS with the minimum Go from `go.mod` and the latest, a Windows test run
(informational until it has been seen passing), every fuzz target for 30s,
a benchstat comparison with the base branch (in the job summary of pull
requests), the VS Code extension checks and a Linux VSIX, and the Neovim
end-to-end test.
