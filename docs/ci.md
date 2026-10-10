# CI (`.github/workflows/ci.yml`)

Every push to `main` and every pull request runs two jobs: `test`, then
`build-and-scan` (once `test` passes).

## `test`

`go vet`, a `gofmt -l` check (fails if anything isn't formatted), `go
build ./...`, then `go test -race ./...` — the same commands `make
build`/`make vet`/`make test-race` run locally, so a change that passes
locally passes here too, and vice versa.

## `build-and-scan`

Runs once per example service (`heroes`, `villains`, via a matrix), each
leg:

1. **Builds the real production image** — `examples/<service>/Dockerfile`,
   the distroless multi-stage build — not the Tilt-only dev image under
   `examples/deploy/tilt/`, which only exists to make the local
   live-reload loop fast and is never what ships (see
   [docs/tilt-dev.md](tilt-dev.md)). The image is loaded into the
   runner's local Docker daemon and **never pushed anywhere** — there's
   no registry wired into this pipeline at all, deliberately; add one
   (and the credentials/`docker/login-action` step it needs) only when
   there's an actual place to push to.
2. **Scans it with [Trivy](https://trivy.dev/)** for CRITICAL/HIGH/MEDIUM
   vulnerabilities, in two output formats from the same underlying scan:
   - **SARIF**, uploaded to the repo's Security tab (Code scanning
     alerts) via `github/codeql-action/upload-sarif`. This step is
     `continue-on-error: true`, and a following `if: always()` step
     writes its real outcome (`steps.sarif-upload.outcome`, the
     pre-continue-on-error result) to the job summary — so a failure
     here is never silent just because the job overall stays green.
     On **this** repo specifically it does fail: a personal
     (`User`-owned, not organization-owned) GitHub account can't enable
     code scanning on a **private** repo at all — GitHub Advanced
     Security is only purchasable at the organization/enterprise level,
     so there's no setting here to flip. Moving the repo to an org with
     GHAS, or making it public, would change that; this workflow needs
     no changes either way.
   - **A plain table**, uploaded as a downloadable workflow artifact
     (`trivy-report-<service>`) — the deliverable that works regardless
     of GHAS, repo visibility, or anyone's Security-tab permissions.

The scan is **report-only** (`exit-code: '0'`): a finding never fails
the build. That's a deliberate starting posture, not an oversight — see
"Known limitations" below for how to change it.

## Why no registry push

The task this pipeline was built for was explicit: build and scan,
don't push. Buildx's `load: true` (load into the local daemon) rather
than `push: true` is the entire mechanism; nothing else needed to
change to support that. Wiring in a real push is a few added lines
(`docker/login-action` plus `push: true` and a real tag) whenever
there's a registry to push to — not a redesign of this file.

## Known limitations worth stating plainly

- **Report-only, not a gate.** A CRITICAL vulnerability in a dependency
  does not fail CI today. If you want a hard gate instead, change
  `exit-code: '0'` to `'1'` on the two `trivy-action` steps for the
  severities that should block (e.g. `severity: CRITICAL` with
  `exit-code: '1'` as a separate, earlier step, keeping a second
  non-blocking scan at the broader severity range for the full report).
- **SARIF/Security-tab integration can't work on this repo as currently
  owned/visible.** Not a config bug — a personal account can't enable
  GitHub Advanced Security on a private repo, period. The job summary
  says so plainly on every run where it fails; the table-artifact
  report is the deliverable guaranteed to produce something readable
  regardless.
- **No registry push, by design** (see above) — this pipeline proves an
  image builds and what's in it, not that it's deployable anywhere.
- **The two example services' own `Dockerfile`s are what's scanned.**
  A future third example would need its own matrix entry (just adding
  its name to the `matrix.service` list — the steps are already
  generic over `${{ matrix.service }}`).
