---
type: internal
area: foundation
title: Project base and first spike (Milestone 0, part 1)
authors: Claude, with the repository owner
branch: feature/foundation
cr:
after_the_fact: false
---

## What changed and why

The repository was empty. This sets up the base every later milestone builds on, and the
first of five experiments that confirm the design before real code is written.

- Go module, a `cicada` binary with `version` and `help`, and a build that stamps the
  version and commit.
- Makefile targets: `build`, `test`, `lint`, `vuln`, `cross`.
- CI workflow: tests on Linux, macOS and Windows; format check, `staticcheck`,
  `govulncheck` and cross-builds.
- Company template files: git hooks, `.gitattributes`, secret ignore rules,
  `.claude/settings.json`, `scripts/version.sh`.
- Docs: `README.md`, `CLAUDE.md`, the action plan, spike status, onboarding baseline.
- Spike S3 (`spikes/s3-manifest`): creates a least-privilege GitHub App from a CLI through
  the App manifest flow.

## How it works

`cmd/cicada` is a thin entry point; `run` takes its arguments and output streams so it can
be tested. `internal/platform/buildinfo` holds the version set with `-ldflags`.

The spike listens on a random loopback port, shows a page whose form posts the manifest
to GitHub, checks the `state` value on the callback, exchanges the one-time code for the
App credentials and stores them owner-only in the user config folder. It answers only
requests addressed to its own loopback address, sets a strict content security policy,
never prints the private key and shows the browser only generic errors.

## Tests added and results

Run on macOS arm64, Go 1.27.4, 2026-11-21:

| Check | Result |
|---|---|
| `go test -race ./...` | Pass (3 packages) |
| `gofmt`, `go vet`, `staticcheck` | Clean |
| `govulncheck` | No vulnerabilities |
| `make cross` (linux amd64/arm64, darwin amd64/arm64, windows amd64) | All build |
| `./bin/cicada version` | Prints the stamped version |

Spike tests cover: least-privilege manifest for org and personal owners, input
validation, foreign `Host` refused, bad or missing `state` and `code` refused, replayed
callback refused, credentials stored with owner-only permissions, key never sent to the
browser, API failure details hidden from the browser.

Spike S3 was then run against GitHub for a personal account on 2026-11-21: the loopback
callback and the repo-scope permission set were accepted and the App was created (see
`documents/Spikes.md`).

**Not verified:** the spike for an organisation owner and on a headless machine; the CI
workflow (no remote connected, never run); tests on Linux and Windows (only cross-built).

## Bugs fixed

None.

## Notes for users / upgrade steps

Nothing usable yet. The binary prints its version only.

## Technical notes

- No `VERSION` file: it is created through `/promote`. Builds are labelled `0.0.0-dev`
  until then.
- Licence: Apache-2.0, `LICENSE` added from the official text.
- CI tool versions are pinned; actions are pinned by tag, to be pinned by SHA before the
  first public release.

## Suggestions

- Run spike S3 on a test account, then write the S1/S2 scale set spike with its
  credentials.
- Pin the CI actions by commit SHA now that the repository is public.
