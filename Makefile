BINARY_EXT := $(if $(filter Windows_NT,$(OS)),.exe,)
BINARY := varco$(BINARY_EXT)

.PHONY: build test lint vuln cover tidy fmt

build:
	mkdir -p bin
	go build -trimpath -o bin/$(BINARY) ./cmd/varco

test:
	go test -race ./...

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

cover:
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

tidy:
	go mod tidy

fmt:
	gofmt -w $$(find cmd internal -type f -name '*.go' 2>/dev/null)
