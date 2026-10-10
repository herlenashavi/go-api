.PHONY: build test test-race vet fmt lint run tidy tilt-up tilt-down tilt-validate

build:
	go build ./...

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

# Run the worked example (examples/heroes) with no external dependencies.
run:
	cd examples/heroes && go run .

# Run both example services in a local Kubernetes cluster. See
# docs/tilt-dev.md — requires `minikube start --container-runtime=docker`
# first (the flag matters; see that doc). The Tiltfile and everything it
# orchestrates live under examples/ (example-app-specific, not framework
# code), so these targets run from there.
tilt-up:
	cd examples && tilt up

tilt-down:
	cd examples && tilt down

# Validate the Tiltfile and k8s manifests without a running cluster/daemon
# (requires kubeconform and hadolint; see docs/tilt-dev.md).
tilt-validate:
	cd examples && tilt ci || true
	kubeconform -summary examples/deploy/k8s/*.yaml
	hadolint --failure-threshold error examples/deploy/tilt/*.Dockerfile examples/*/Dockerfile
