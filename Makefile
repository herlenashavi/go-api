.PHONY: build test test-race vet fmt lint run tidy

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
