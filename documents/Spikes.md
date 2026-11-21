# Milestone 0 spikes

Status of the open questions from section 7 of the [Action Plan](Action%20Plan.md).
"Desk" means read from documentation; "Live" means run against GitHub or real hardware.
Only live results count as verified.

| ID | Question | Desk | Live |
|---|---|---|---|
| S1 | GitHub App for repo-level scale sets on a personal account | Partly | Not run |
| S2 | Job spreading across hosts that share labels | No answer found | Not run |
| S3 | App manifest flow from a CLI with a loopback callback | Done | **Ready to run** |
| S4 | macOS VMs with `vz` | Not started | Not run |
| S5 | Windows process isolation and cleanup | Not started | Not run (needs a Windows machine) |

## S3: App manifest flow

**Desk findings (GitHub docs, 2026-11-21)**

- The browser posts a `manifest` form field to `https://github.com/settings/apps/new`
  (personal) or `https://github.com/organizations/<org>/settings/apps/new`, with an
  optional `state` query parameter that GitHub echoes back.
- Only `url` is required in the manifest. `hook_attributes` is optional, so the App can be
  created with no webhook, which fits Cicada's outbound-only design.
- GitHub redirects to `redirect_url` with a temporary `code`; `POST
  /app-manifests/{code}/conversions` returns the App id, slug and private key. All steps
  must finish within one hour.
- The docs do not say whether a loopback `redirect_url` is accepted. **This is what the
  live run must show.**
- Permissions planned (same sets GitHub documents for its own runner controller):
  org-level runners need `organization_self_hosted_runners: write` and `metadata: read`;
  repo-level runners need `administration: write` and `metadata: read`.
  `administration: write` is broad (it also covers repo settings); this is a GitHub
  limitation to state openly in the security docs.

**Spike program:** `spikes/s3-manifest`. Unit-tested against a fake GitHub API; not yet
run against GitHub.

Personal account (repo-level runners):

```bash
go run ./spikes/s3-manifest
```

Organisation (org-level runners), replacing the name:

```bash
go run ./spikes/s3-manifest -org YOUR-TEST-ORG
```

It prints a local URL, you press one button, GitHub asks you to confirm the App, and the
credentials are saved in your user config folder under `cicada-spike/` (owner-only, never
printed, outside the repo). It prints the link to delete the App afterwards.

**To record after the live run:** did the loopback redirect work; did GitHub accept the
permission set for a personal and an org owner; anything the confirmation screen shows
that would surprise a user.

## S1 and S2: scale sets

Need the App from S3 installed on a test account and a test org with at least one
throwaway repo. The spike program will be written once S3 has run, because it uses those
credentials. S2 also needs two machines, or two processes with different scale set names
on one machine.

## S4 and S5

Not started. S4 can run on this Apple Silicon Mac but downloads a macOS restore image
(about 15 GB), so it waits for a go-ahead. S5 needs a Windows machine.
