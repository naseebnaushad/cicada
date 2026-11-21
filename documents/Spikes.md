# Milestone 0 spikes

Status of the open questions from section 7 of the [Action Plan](Action%20Plan.md).
"Desk" means read from documentation; "Live" means run against GitHub or real hardware.
Only live results count as verified.

| ID | Question | Desk | Live |
|---|---|---|---|
| S1 | GitHub App for repo-level scale sets on a personal account | Partly | **Ready to run** |
| S2 | Job spreading across hosts that share labels | No answer found | **Ready to run** (same program) |
| S3 | App manifest flow from a CLI with a loopback callback | Done | **Works** for a personal account (2026-11-21); organisation owner not run |
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

**Live result, personal account (2026-11-21, macOS)**

- GitHub accepted `http://127.0.0.1:<port>/callback` as the `redirect_url` and redirected
  back to it with the code. **A loopback callback works.**
- GitHub accepted the manifest with no webhook and the repo-scope permission set
  (`administration: write`, `metadata: read`) for a user-owned App.
- The code exchange returned the App id, slug and private key; both files were stored with
  mode 0600 in a 0700 folder; the key was not printed.
- One button on the local page, one confirmation on GitHub. The App is created but not
  yet installed on any repo; installing is a second step.

**Conclusion:** scenario ON-01/ON-02 onboarding can use the manifest flow as planned. No
fallback needed for personal accounts.

**Still to run:** the same flow with `-org` on a test organisation (org-scope
permissions), and the headless case (ON-05) where the browser is on another machine, in
which a loopback callback cannot work as is.

## S1 and S2: scale sets

**Spike program:** `spikes/s1-scaleset`. Unit-tested with fakes; **not yet run against
GitHub.** It uses the App credentials stored by S3, finds the App's installation,
registers (or reuses) a scale set, long-polls for jobs, starts one throwaway Docker
container per assigned job and deletes the scale set on exit.

**Before running**

1. Create an empty private repo to experiment in, for example `cicada-sandbox`.
2. Install the App on that repo only ("Only select repositories"):
   `https://github.com/apps/<app-slug>/installations/new`.
3. Start Docker.
4. Add this workflow to the sandbox repo as `.github/workflows/spike.yml`:

```yaml
name: spike
on: workflow_dispatch
jobs:
  hello:
    runs-on: cicada-spike
    strategy:
      matrix:
        n: [1, 2, 3, 4]
    steps:
      - run: echo "job ${{ matrix.n }} on $(hostname)" && sleep 20
```

**S1: one scale set on a personal repo**

```bash
go run ./spikes/s1-scaleset -url https://github.com/OWNER/cicada-sandbox
```

Then start the workflow from the repo's Actions tab. Pass: the scale set is created with
only the App's `administration: write` permission, the four jobs run two at a time
(`-max 2`) in containers that disappear afterwards, and Ctrl-C deletes the scale set.

**S2: two scale sets sharing a label**

Run two copies in two terminals (or on two machines), then start the workflow:

```bash
go run ./spikes/s1-scaleset -url https://github.com/OWNER/cicada-sandbox -name spike-a -max 1
```

```bash
go run ./spikes/s1-scaleset -url https://github.com/OWNER/cicada-sandbox -name spike-b -max 1
```

Both register the label `cicada-spike`. The logs show which scale set each job is
assigned to. Questions to answer: are jobs spread across both; when one is full, does a
waiting job move to the other or stay stuck behind it; what happens to queued jobs when
one copy stops.

`-backend none` registers and listens without starting runners, to watch assignment only.
Jobs assigned that way stay queued until the run is cancelled on GitHub.

**Desk note:** the client's README says several labels per scale set work on github.com;
on GitHub Enterprise Server they need version 3.18+ and a feature flag, otherwise only the
scale set name is kept as a label. Scenario ON-07 must account for that.

## S4 and S5

Not started. S4 can run on this Apple Silicon Mac but downloads a macOS restore image
(about 15 GB), so it waits for a go-ahead. S5 needs a Windows machine.
