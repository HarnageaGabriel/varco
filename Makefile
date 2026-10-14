.PHONY: build test lint vuln cover tidy fmt docs docs-check release-check

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

release-check:
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser check; \
		status=$$?; \
		if [ "$$status" -eq 2 ]; then \
			echo "release-check: GoReleaser reported the known deprecated brews option" >&2; \
		elif [ "$$status" -ne 0 ]; then \
			exit "$$status"; \
		fi; \
		goreleaser release --snapshot --clean; \
	else \
		echo "release-check: goreleaser is not installed; install GoReleaser v2 first" >&2; \
		exit 1; \
	fi
