.PHONY: build test lint vuln cover tidy fmt docs docs-check

build:
	go build -trimpath ./...

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
	gofumpt -w cmd internal tools

docs:
	go run ./tools/docsgen -out docs/diagnostics

docs-check:
	go run ./tools/docsgen -out docs/diagnostics -check
