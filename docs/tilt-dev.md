# Local Kubernetes dev loop (Tilt + minikube)

This repo's two example services — `heroes` and `villains`, talking to
each other over gRPC (see [docs/grpc.md](grpc.md)) — can run in a real,
local Kubernetes cluster with a fast edit/rebuild loop, using
[Tilt](https://tilt.dev) against [minikube](https://minikube.sigs.k8s.io).
This is a demonstration of operating go-api services in a
Kubernetes-shaped environment, not a new framework feature — nothing in
`pkg/` changed for this; `examples/deploy/` and `examples/Tiltfile` are
additive, and live under `examples/` because all of it is specific to
these two example services, not shared framework config.

## Prerequisites

```shell
brew install docker minikube tilt kubectl   # macOS; see each tool's own install docs otherwise
```

Docker Desktop (or another Docker-compatible daemon) needs to actually be
**running** — `docker info` should succeed — before `minikube start`.

## Run it

```shell
minikube start --container-runtime=docker
cd examples && tilt up   # or: make tilt-up
```

**`--container-runtime=docker` is not optional.** Tilt's whole
zero-push, build-straight-into-the-cluster trick for minikube only works
when minikube's node runs Docker as its container runtime — it shares
its image store directly with the Docker daemon Tilt just built with, so
nothing needs pushing anywhere. If minikube ends up on `containerd`
instead (its `auto` runtime selection can land there depending on
platform/version — check with `minikube profile list`), that sharing
doesn't exist, and Tilt falls back to pushing the image to a registry —
defaulting to Docker Hub for an unqualified name like `go-api/heroes`,
which fails:

```
Build Failed: docker push: pushing image "docker.io/go-api/heroes":
push access denied, repository does not exist or may require authorization
```

This isn't a bug in this example suite's Tiltfile or Dockerfiles — the image
*builds* successfully every time; only the push step fails, because
there's nowhere for it to usefully go. If you hit this on a cluster you
already created without the flag, the fix is `minikube delete &&
minikube start --container-runtime=docker` (fine for this repo's demo
cluster — it carries no state worth keeping). This exact failure mode,
diagnosis, and fix were confirmed by actually hitting it against a real
`containerd`-runtime minikube cluster and resolving it this way.

Tilt prints a URL (default `http://localhost:10350`) with a live view of
both services building and deploying. Once both show green:

```shell
curl localhost:8080/health/liveness          # heroes, via Tilt's port_forward
curl localhost:8081/health/liveness          # villains
grpcurl -plaintext localhost:9091 grpc.health.v1.Health/Check
```

Everything in [`examples/heroes/README.md`](../examples/heroes/README.md)
and [`examples/villains/README.md`](../examples/villains/README.md) — minting
tokens, creating heroes/villains, the nemesis route, RBAC/ABAC denials —
works identically against these forwarded ports. The services don't know
or care that they're in Kubernetes instead of running bare with `go run`.

`cd examples && tilt down` (or `make tilt-down`) tears everything down;
`minikube delete` removes the cluster entirely.

## What's actually demonstrated here

### 1. Fast iteration for a compiled binary

Rebuilding a Docker image from source on every save is slow for Go — most
of that time is the compiler, not Docker. The Tiltfile instead:

1. Runs `go build` **on the host** via `local_resource` (your normal build
   cache, no container overhead; the command runs with `dir='..'` so it
   executes from the repo root, where `go.mod` lives, even though the
   Tiltfile itself is under `examples/`).
2. Packages *only* that already-built binary into a minimal Alpine image
   (`examples/deploy/tilt/*.Dockerfile` — **not** the production
   `examples/*/Dockerfile`, which compile inside Docker as a real pipeline
   would; see those files' doc comments for why the two differ).
3. On every subsequent save, `live_update` skips the image rebuild
   entirely: it `sync()`s the new binary straight into the running
   container and restarts just the process, via the
   [`restart_process`](https://github.com/tilt-dev/tilt-extensions/tree/master/restart_process)
   extension's `docker_build_with_restart` (plain `restart_container()` is
   deprecated for Kubernetes resources as of the Tilt version this was
   written against — this example suite's Tiltfile found that out by
   actually running `tilt ci` against it, not from reading docs).

Edit a `.go` file under `examples/heroes` or `pkg`, save, and the running
pod restarts with your change in roughly the time `go build` takes — no
image rebuild, no `kubectl apply`, no pod reschedule.

### 2. Cedar policy hot reload, wired through Kubernetes

Both services already poll for policy file changes
(`authz.PolicyStore.WatchAndReload`, wired into `main.go` for exactly this
demo). Here, `policies/` is a `ConfigMap`-backed volume mount
(`examples/deploy/k8s/heroes.yaml` / `villains.yaml`), and that
`ConfigMap` is generated **from the real `.cedar` files' actual content**
by the Tiltfile (`read_file` + a small indent helper) — there is no copy
of the policy text living in YAML to drift out of sync.

Edit `examples/heroes/policies/heroes.cedar`, save:

1. Tilt notices (`read_file` registers the path as a Tiltfile dependency)
   and re-runs the Tiltfile, regenerating and re-applying the `ConfigMap`.
2. Kubelet syncs the mounted volume into the running pod — on its own
   schedule, typically **up to ~60–90 seconds**, not instantly. This is a
   real kubelet behavior, not a limitation of this setup.
3. `PolicyStore.WatchAndReload`'s 5-second poll picks up the change from
   the now-updated file on disk and swaps in the new policy set.

So end-to-end latency for a policy change here is dominated by step 2,
kubelet's sync period — worth knowing so you don't assume something's
broken when a policy edit doesn't take effect in the first few seconds.
(Running `go run .` directly, outside Kubernetes, skips steps 1–2 entirely
and reacts within one poll interval.)

## Layout

Everything here is example-app specific, so it all lives under
`examples/` rather than the repo root:

```
examples/
  Tiltfile                  # orchestrates everything below
  deploy/
    tilt/
      heroes.Dockerfile     # dev-loop image: copies a host-built binary
      villains.Dockerfile   # same, for villains
      bin/                  # local_resource's build output (gitignored)
    k8s/
      namespace.yaml        # go-api-demo namespace
      secret.yaml           # shared dev JWT secret (see its own comment)
      heroes.yaml           # Deployment + Service
      villains.yaml         # Deployment + Service
```

The Cedar policy `ConfigMap`s aren't static files here — see §2 above.

## Validating without a running cluster

Reviewing or extending this setup without Docker/minikube available doesn't
block much — these checks don't need either:

```shell
cd examples
tilt ci       # parses and loads the Tiltfile; fails at the cluster-connect
              # step with no cluster configured, which is as far as this
              # validates without one — but a Tiltfile syntax/logic error
              # would fail *before* that point, which is what this catches
kubeconform deploy/k8s/*.yaml   # validates manifests against the k8s OpenAPI schema
hadolint deploy/tilt/*.Dockerfile */Dockerfile
```

`make tilt-validate` runs all three. This repo's full `tilt up` loop —
both images building and deploying, both pods reaching `Running`, the
`/health` endpoints and the gRPC health check responding, the
cross-service `nemesis` call working end-to-end through the cluster, and
a `live_update` edit reaching the running pod in ~1 second with zero
Kubernetes-level restarts — has been run for real against a
`--container-runtime=docker` minikube cluster; it is not merely inferred
from the static checks above.
