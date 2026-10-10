# -*- mode: Python -*-
#
# Tiltfile for go-api's example services (heroes, villains) running in a
# local Kubernetes cluster — minikube, by convention for this repo, though
# nothing here is minikube-specific. Prerequisites and a full walkthrough
# live in docs/tilt-dev.md; the short version:
#
#   minikube start
#   tilt up
#
# Two things worth understanding before reading the rest of this file:
#
# 1. Fast iteration for a compiled binary. Rebuilding a Docker image on
#    every save is slow for Go — most of that time is the compiler, not
#    Docker. So each service is built in two stages: local_resource runs
#    `go build` on the host (fast, uses your normal build cache), and
#    docker_build_with_restart (the tilt-extensions `restart_process`
#    helper — plain live_update `restart_container()` is deprecated for
#    k8s resources as of the Tilt version this was written against)
#    packages *only* that already-built binary into a minimal image. On
#    every subsequent save, live_update skips the image rebuild entirely:
#    it syncs the new binary straight into the running container and
#    restarts just the process. The Dockerfiles under deploy/tilt/ are
#    Alpine-based specifically because that restart mechanism needs a
#    shell in the image (see their doc comments); they are NOT the
#    production Dockerfiles (examples/heroes/Dockerfile and
#    examples/villains/Dockerfile are, and compile inside Docker as a
#    real build pipeline would).
#
# 2. Cedar policy hot reload, wired through Kubernetes. Each service's
#    PolicyStore.WatchAndReload (pkg/authz) already polls for changed
#    policy files — examples/heroes/main.go and examples/villains/main.go
#    both call it. Here, that directory is a ConfigMap-backed volume
#    mount (see deploy/k8s/*.yaml), and the ConfigMaps below are
#    generated from the actual policies/*.cedar files' real content —
#    not a copy kept in sync by hand. Edit a .cedar file, save: Tilt
#    notices (read_file tracks the file as a Tiltfile dependency),
#    regenerates and re-applies the ConfigMap, kubelet syncs the mounted
#    volume into the running pod, and the service's own watcher picks up
#    the change from there — no rebuild, no restart. See docs/tilt-dev.md
#    for realistic end-to-end latency (it's dominated by kubelet's sync
#    period, not anything in this file).

load('ext://restart_process', 'docker_build_with_restart')

GOARCH = str(local('go env GOARCH')).strip()

k8s_yaml(['deploy/k8s/namespace.yaml', 'deploy/k8s/secret.yaml'])

def policy_configmap(name, cedar_file):
    """Builds a ConfigMap manifest embedding cedar_file's current content,
    so the policies a pod sees are always exactly what's on disk right
    now — see this file's header comment."""
    content = str(read_file(cedar_file))
    indented = "\n".join(["    " + line for line in content.split("\n")])
    return blob("""apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: go-api-demo
data:
  %s: |
%s
""" % (name, cedar_file.split("/")[-1], indented))

k8s_yaml(policy_configmap('heroes-policies', 'examples/heroes/policies/heroes.cedar'))
k8s_yaml(policy_configmap('villains-policies', 'examples/villains/policies/villains.cedar'))

k8s_yaml(['deploy/k8s/villains.yaml', 'deploy/k8s/heroes.yaml'])

# --- villains: gRPC server, built and deployed first (heroes calls it) ---

local_resource(
    'villains-binary',
    'mkdir -p deploy/tilt/bin && CGO_ENABLED=0 GOOS=linux GOARCH=%s go build -o deploy/tilt/bin/villains ./examples/villains' % GOARCH,
    deps=['pkg', 'gen', 'examples/villains', 'go.mod', 'go.sum'],
    labels=['build'],
)

docker_build_with_restart(
    'go-api/villains',
    '.',
    dockerfile='deploy/tilt/villains.Dockerfile',
    only=['./deploy/tilt/bin/villains'],
    entrypoint=['/app/villains'],
    live_update=[sync('./deploy/tilt/bin/villains', '/app/villains')],
)

k8s_resource(
    'villains',
    port_forwards=['8081:8081', '9091:9091'],
    labels=['services'],
)

# --- heroes: HTTP API, calls villains over gRPC ---

local_resource(
    'heroes-binary',
    'mkdir -p deploy/tilt/bin && CGO_ENABLED=0 GOOS=linux GOARCH=%s go build -o deploy/tilt/bin/heroes ./examples/heroes' % GOARCH,
    deps=['pkg', 'gen', 'examples/heroes', 'go.mod', 'go.sum'],
    labels=['build'],
)

docker_build_with_restart(
    'go-api/heroes',
    '.',
    dockerfile='deploy/tilt/heroes.Dockerfile',
    only=['./deploy/tilt/bin/heroes'],
    entrypoint=['/app/heroes'],
    live_update=[sync('./deploy/tilt/bin/heroes', '/app/heroes')],
)

k8s_resource(
    'heroes',
    port_forwards=['8080:8080'],
    resource_deps=['villains'],
    labels=['services'],
)
