# Local Kubernetes dev loop (Tilt + minikube)

This repo's two example services — `heroes` and `villains`, talking to
each other over gRPC (see [docs/grpc.md](grpc.md)) — can run in a real,
local Kubernetes cluster with a fast edit/rebuild loop, using
[Tilt](https://tilt.dev) against [minikube](https://minikube.sigs.k8s.io).
This is a demonstration of operating go-api services in a
Kubernetes-shaped environment, not a new framework feature — nothing in
`pkg/` changed for this; `deploy/` and the root `Tiltfile` are additive.

## Prerequisites

```shell
brew install docker minikube tilt kubectl   # macOS; see each tool's own install docs otherwise
```

Docker Desktop (or another Docker-compatible daemon) needs to actually be
**running** — `docker info` should succeed — before `minikube start`.
This matters enough to call out explicitly: the Tiltfile and manifests in
this repo were authored and syntax-validated (`tilt ci`, `kubeconform`,
`hadolint` — see below) without a running Docker daemon available in the
sandbox that wrote them, so the wiring is correct by inspection and static
validation, but **the full `tilt up` loop has not been run end-to-end in
this repository's CI/sandbox**. Please treat the first run as the real
first run, and open an issue if something doesn't match this doc.

## Run it

```shell
minikube start
tilt up
```

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

`tilt down` tears everything down; `minikube delete` removes the cluster
entirely.

## What's actually demonstrated here

### 1. Fast iteration for a compiled binary

Rebuilding a Docker image from source on every save is slow for Go — most
of that time is the compiler, not Docker. The Tiltfile instead:

1. Runs `go build` **on the host** via `local_resource` (your normal build
   cache, no container overhead).
2. Packages *only* that already-built binary into a minimal Alpine image
   (`deploy/tilt/*.Dockerfile` — **not** the production
   `examples/*/Dockerfile`, which compile inside Docker as a real pipeline
   would; see those files' doc comments for why the two differ).
3. On every subsequent save, `live_update` skips the image rebuild
   entirely: it `sync()`s the new binary straight into the running
   container and restarts just the process, via the
   [`restart_process`](https://github.com/tilt-dev/tilt-extensions/tree/master/restart_process)
   extension's `docker_build_with_restart` (plain `restart_container()` is
   deprecated for Kubernetes resources as of the Tilt version this was
   written against — this repo's Tiltfile found that out by actually
   running `tilt ci` against it, not from reading docs).

Edit a `.go` file under `examples/heroes` or `pkg`, save, and the running
pod restarts with your change in roughly the time `go build` takes — no
image rebuild, no `kubectl apply`, no pod reschedule.

### 2. Cedar policy hot reload, wired through Kubernetes

Both services already poll for policy file changes
(`authz.PolicyStore.WatchAndReload`, wired into `main.go` for exactly this
demo). Here, `policies/` is a `ConfigMap`-backed volume mount
(`deploy/k8s/heroes.yaml` / `villains.yaml`), and that `ConfigMap` is
generated **from the real `.cedar` files' actual content** by the
Tiltfile (`read_file` + a small indent helper) — there is no copy of the
policy text living in YAML to drift out of sync.

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

```
Tiltfile                        # orchestrates everything below
deploy/
  tilt/
    heroes.Dockerfile           # dev-loop image: copies a host-built binary
    villains.Dockerfile         # same, for villains
    bin/                        # local_resource's build output (gitignored)
  k8s/
    namespace.yaml              # go-api-demo namespace
    secret.yaml                 # shared dev JWT secret (see its own comment)
    heroes.yaml                 # Deployment + Service
    villains.yaml               # Deployment + Service
```

The Cedar policy `ConfigMap`s aren't static files here — see §2 above.

## Validating this without a running cluster

In case you're reviewing this setup (or extending it) without Docker/minikube
running, these checks don't need either:

```shell
tilt ci       # parses and loads the Tiltfile; fails at the cluster-connect
              # step with no cluster configured, which is as far as this
              # validates without one — but a Tiltfile syntax/logic error
              # would fail *before* that point, which is what this catches
kubeconform deploy/k8s/*.yaml   # validates manifests against the k8s OpenAPI schema
hadolint deploy/tilt/*.Dockerfile examples/*/Dockerfile
```
