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
# first (the flag matters; see that doc).
tilt-up:
	tilt up

tilt-down:
	tilt down

# Validate the Tiltfile and k8s manifests without a running cluster/daemon
# (requires kubeconform and hadolint; see docs/tilt-dev.md).
tilt-validate:
	tilt ci || true
	kubeconform -summary deploy/k8s/*.yaml
	hadolint --failure-threshold error deploy/tilt/*.Dockerfile examples/*/Dockerfile
