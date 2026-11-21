# Cicada — Claude instructions

The company engineering rules (installed globally by `claude-standards`) apply to this
repo. Work freely within them. If they are not loaded in your session, tell the developer
to run the `claude-standards` setup script, and until then follow the safety essentials
below.

**Safety essentials (always, even without the company rules):**
- Never read, print, log or commit secrets; don't open `.env*`, keys or certificates
  (use `.env.example`). Never send code or data to an outside service without approval.
- Never commit on, push to, merge into or rebase `main`, `develop` or any `build/`
  branch; never merge a PR yourself; never force-push. Work on a `feature/` or `fix/`
  branch and open a PR.
- Never touch production systems, data or credentials. Ask before deleting data,
  migrations, deployments, CI or infrastructure changes, or anything hard to undo.
- `VERSION`, tags and `documents/CHANGELOG.md` change only through `/promote`.

**Every session:** if docs are missing (`README.md`, `documents/README.md`,
`.env.example`, this file), write them first. If `documents/AI Onboarding.md` is missing,
run `/onboard`. If `REFACTOR-NEEDED.md` exists, remind the developer first.

## What this project is

Cicada makes self-hosted CI/CD simple: one Go binary that turns a Linux, macOS or Windows
machine into a pool of single-use GitHub Actions runners, built on GitHub runner scale
sets. It is meant to be open source. Status: Milestone 0 (foundation and spikes); no
`VERSION` file yet. The plan, scenarios and milestones are in
`documents/Action Plan.md`; read it before designing anything.

## Layout

```
cmd/cicada/                  CLI entry point and composition root
internal/platform/buildinfo  version stamp
spikes/<id>-<topic>/         spike programs for the open questions in documents/Spikes.md
scripts/version.sh           build label (company script, copied in)
documents/                   versioned docs (company convention)
```

Packages still to come (`internal/domain`, `internal/app`, `internal/ports`,
`internal/adapters/...`) are described in section 3.2 of the action plan. Create a
package only when a milestone needs it.

## Build, run, test

```bash
make build
```
```bash
./bin/cicada version
```
```bash
make test
```
```bash
make lint && make vuln && make cross
```

There are no live or integration tests yet. Spike programs that talk to GitHub are run by
the developer, not by Claude, because they create things on a GitHub account.

## External services

| Service | Purpose | Environment | Variables (names only) |
|---|---|---|---|
| GitHub (web and API) | Create the App, register scale sets, receive jobs | A test account or test org only | none yet |

## Project decisions

- Go, toolchain version from `go.mod`. Standard library first; every dependency is
  licence-checked (section 11 of the company rules) and pinned.
- Clean architecture with ports and adapters; `domain` and `app` never import adapters.
- Cicada is a runner layer over GitHub Actions, not its own CI engine.
- Not used: Tart (licence limits), Kubernetes.
- Spike code lives in `spikes/` and is not imported by product code; what survives is
  rewritten into `internal/`.
- Secrets created by spikes are stored outside the repo (the user config folder),
  owner-only, and never printed.

## Gotchas

- `scripts/version.sh label` fails until a `VERSION` file exists; the Makefile falls back
  to `0.0.0-dev`.
- `main` and `develop` do not exist yet. The first commit and how those branches are
  created is the developer's decision.
- The macOS VM backend will need cgo and a signed helper; everything else must stay
  buildable with `CGO_ENABLED=0`.
