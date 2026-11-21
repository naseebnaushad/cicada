# Cicada

Simple self-hosted CI/CD. Cicada is one Go binary that turns a Linux, macOS or Windows
machine into a pool of clean, single-use GitHub Actions runners for every repo you
choose, with one command and no per-project setup.

> **Status: Milestone 0 (foundation and spikes).** The binary only prints its version.
> Nothing here runs CI jobs yet. The design and roadmap are in
> [documents/Action Plan.md](documents/Action%20Plan.md).

## What it will do

- `cicada up` creates a least-privilege GitHub App in one browser step, registers the
  machine and installs itself as a service.
- Workflows opt in with `runs-on: cicada`.
- Each job gets a fresh container, VM or clean process that is destroyed afterwards.
- Zero runners when idle, outbound HTTPS only, no database, no central server.

## Requirements

- Go 1.27 or newer
- `make` (optional; the commands below can also be run by hand)
- For `make lint` and `make vuln`:

```bash
go install honnef.co/go/tools/cmd/staticcheck@2026.2.2
```

```bash
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
```

## Build and run

```bash
make build
```

```bash
./bin/cicada version
```

## Test and check

```bash
make test
```

```bash
make lint
```

```bash
make vuln
```

```bash
make cross
```

`make cross` proves the binary builds for Linux, macOS and Windows.

## Layout

```
cmd/cicada/                 CLI entry point
internal/platform/buildinfo version stamped into the binary
spikes/                     throwaway experiments that answer open design questions
scripts/version.sh          build label from git and VERSION
documents/                  plan, spike notes, change notes
.github/workflows/ci.yml    tests on Linux, macOS, Windows; lint and vulnerability scan
```

The full package layout that the milestones fill in is in section 3.2 of the action plan.

## Configuration

Cicada reads no configuration or environment variables yet. `.env.example` will list
every variable once there are any.

## Documents

See [documents/README.md](documents/README.md).

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
