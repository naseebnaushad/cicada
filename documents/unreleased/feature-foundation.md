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

- Spike S1/S2 (`spikes/s1-scaleset`): registers a runner scale set with that App,
  long-polls for jobs and runs each in a throwaway Docker container. First dependencies:
  `github.com/actions/scaleset` v0.5.0 (MIT), `github.com/golang-jwt/jwt/v5` v5.3.1 (MIT)
  and, indirectly, `github.com/google/uuid` v1.6.0 (BSD-3-Clause).

## How it works

`cmd/cicada` is a thin entry point; `run` takes its arguments and output streams so it can
be tested. `internal/platform/buildinfo` holds the version set with `-ldflags`.

The spike listens on a random loopback port, shows a page whose form posts the manifest
to GitHub, checks the `state` value on the callback, exchanges the one-time code for the
App credentials and stores them owner-only in the user config folder. It answers only
requests addressed to its own loopback address, sets a strict content security policy,
never prints the private key and shows the browser only generic errors.

The scale set spike signs a short-lived App token, looks up the App's installation for the
target repo or organisation, and hands the credentials to GitHub's scale set client. Its
scaler claims offered jobs, starts one runner per assigned job up to a limit, and frees
the slot when the job completes or the runner exits. The single-use runner configuration
is passed to Docker through the environment, never as a command-line argument. Docker is
checked before anything is created on GitHub, and the scale set, session and containers
are cleaned up on exit.

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

Scale set spike tests (fakes, no network): one runner per assigned job capped at the
limit, redelivered messages start nothing twice, every offered job is acquired, completed
jobs and runner exits free capacity, failed starts are not tracked, shutdown removes all
runners, unsafe URLs, labels and container names are refused, the App token verifies and
stays under GitHub's 10-minute limit, a key file readable by other users is refused, API
error bodies are not echoed. Re-run on 2026-11-21: tests, `staticcheck`, `govulncheck` and
cross-builds all pass with the new dependencies.

**Not verified:** the scale set spike against GitHub (needs a sandbox repo with the App
installed, and Docker running; on this machine it stops at "docker is not running" as
designed); the S3 spike for an organisation owner and on a headless machine; the CI
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

- Run the scale set spike on a sandbox repo to answer S1 and S2.
- Pin the CI actions by commit SHA now that the repository is public.
