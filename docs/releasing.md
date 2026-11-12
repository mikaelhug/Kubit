# Releasing

Kubit ships as `Kubit.app`, a menu bar app that runs the daemon. A GitHub Release holds
`Kubit-X.Y.Z-macos-universal.zip`, its Ed25519 signature `.zip.sig` and `SHA256SUMS`.

## Versions

Kubit has its own semver, independent of Talos. Releases are annotated `vX.Y.Z` tags on
`main`; `make build` and the app stamp `git describe --match 'v[0-9]*.[0-9]*.[0-9]*'`.
A weekly release is a minor when any dependency moved a minor, else a patch.

## Build

`make app` (`hack/build-app.sh`) builds the console, `kubit` for darwin arm64 and amd64
(`CGO_ENABLED=0`, joined with `lipo`) and `KubitApp` from `app/`, assembles
`dist/Kubit.app`, signs it and zips it. `hack/build-app.sh release` takes the version from
the exact tag on HEAD and requires `app/release.pub`; a local build is `0.0.0` unless
`KUBIT_VERSION` is set. A universal `KubitApp` needs full Xcode; with the Command Line
Tools only it builds for the host.

| Variable | Effect |
|---|---|
| `KUBIT_SIGN_IDENTITY` | `Developer ID Application: …`; empty or `-` signs ad-hoc |
| `KUBIT_VERSION` | version of a local build |
| `KUBIT_UPDATE_FEED` | release feed the app checks (default: this repo's `releases/latest`) |
| `KUBIT_RELEASE_PUBKEY` | public key put in the app instead of `app/release.pub` |

A Developer ID build is signed with the hardened runtime and a timestamp, inner binary
first. An ad-hoc build is signed without the hardened runtime: macOS kills a downloaded,
ad-hoc, hardened app at launch.

## Release workflow

`.github/workflows/release.yml` runs on every `v*` tag (and builds without publishing on
a manual run): the Go, web and Swift tests, an optional Developer ID signature, an optional
notarization (`notarytool --wait`, must be `Accepted`, then `stapler staple`), the Ed25519
signature with `go run ./hack/release-sign sign`, a verification against `app/release.pub`
that fails the release when it does not match, `SHA256SUMS`, and the GitHub Release. The
release notes are the tag's message.

## Weekly dependency release

`.github/workflows/weekly.yml` runs Mondays at 07:00 UTC. `hack/deps.sh` applies every
patch and minor update and lists the rest for review:

| Component | Source | Verified by | Applied | Review |
|---|---|---|---|---|
| Go toolchain (`go` in go.mod) | go.dev | go.dev checksums | patch | minor |
| Talos machinery | Go proxy | sumdb | patch; a minor from its `.1` | major |
| `k8s.io/*` | Go proxy | sumdb | latest patch of machinery's Kubernetes minor | |
| Other direct Go modules | Go proxy | sumdb, `go mod verify` | patch, minor | prereleases, pseudo-versions |
| npm (`web/`) | npm | lockfile integrity, `npm audit signatures` | within the ranges in package.json | outside them |
| OpenTofu | GitHub releases | cosign signature of `SHA256SUMS` from OpenTofu's release workflow | patch, minor | major |
| Providers | OpenTofu registry | provider signatures, `tofu providers lock` | within the `~>` constraints | major |
| Helm charts | chart repo `index.yaml` | digest, `helm template` at Kubit's Kubernetes version | latest patch of each newer minor, as a step in `internal/tofu/charts.json` | major |
| buildkit image | GitHub releases | `docker buildx imagetools inspect` | patch, minor | major |
| SOPS format, talos-backup, Gateway API, pinned actions | GitHub | | | always |

The jobs:

1. **prepare** commits the changes with a `Bump: <component> <from> <to>` line per change and
   hands the commit to the next jobs as a git bundle. Nothing is pushed.
2. **validate** builds and tests the commit on macOS: `make build`, `make test`, the web tests,
   the OpenTofu template tests (`-tags integration`), the Swift tests and an app build.
3. **ship** tags `vX.Y.Z` at the commit, pushes the tag and fast-forwards `main` through the
   `AUTOMATION_SSH_KEY` deploy key, so the tag starts the release workflow. It fails when
   `main` moved during the run. It runs on schedule, or on a manual run with `ship`.
4. **review** keeps one open issue, *Dependency review*, with the majors and report-only items,
   and closes it when the list is empty.
5. **prune** deletes earlier successful weekly runs in which validate, ship and review were all
   skipped. A run cannot delete itself, so an empty run goes the week after.

Nothing ships until `main` carries a `vX.Y.Z` tag.

## Rollback

*Rollback a weekly release* (`rollback.yml`, manual, on `main`) reverts the bump commit of
the given release, adds its versions to `hack/deps.blocked` so the weekly job skips them,
builds and tests, and tags the next minor. A revert that conflicts fails; roll back by hand.

## Updates in the app

The app checks the release feed at launch, after a wake when the last check is a day old,
and on *Check for Updates*. It installs only when you choose *Install*: it downloads the zip
and its signature to `~/.kubit/updates/<tag>/`, refuses a missing or wrong signature and a
version that is not newer, stops the daemon, replaces the app and relaunches it. When the
app cannot replace itself it reveals the zip in Finder.

## One-time setup

1. `go run ./hack/release-sign keygen`: store the private key as the repository secret
   `KUBIT_RELEASE_KEY` and commit the public key as `app/release.pub`. A lost private key
   means users install the next release by hand.
2. A deploy key with write access, its private half as the secret `AUTOMATION_SSH_KEY`:
   `ssh-keygen -t ed25519 -N '' -f kubit-automation`,
   `gh repo deploy-key add kubit-automation.pub --allow-write -t automation`,
   `gh secret set AUTOMATION_SSH_KEY < kubit-automation`, then delete both files.
3. Optional Developer ID signing and notarization, all six or none: `MACOS_CERT_P12_BASE64`,
   `MACOS_CERT_PASSWORD`, `MACOS_SIGN_IDENTITY`, `AC_API_KEY_ID`, `AC_API_ISSUER_ID`,
   `AC_API_KEY_BASE64`. Without them releases are ad-hoc signed, need *Open Anyway* in
   System Settings once, and ask for local network access again after each update.
4. The first release by hand: `git tag -a v0.1.0 -m "Kubit 0.1.0" && git push origin v0.1.0`.

GitHub stops scheduled workflows in a repository without activity for 60 days.
