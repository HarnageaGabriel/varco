.PHONY: build test bench fuzz lint vuln cover tidy fmt docs docs-check demo release-check verify-reproducible

build:
	go build -trimpath ./...

test:
	go test -race ./...

bench:
	go test -run ^$$ -bench . -benchtime 100ms ./...

fuzz:
	sh scripts/fuzz.sh

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
	go run ./tools/docsgen -out docs/diagnostics -reference-out docs/reference

docs-check:
	go run ./tools/docsgen -out docs/diagnostics -reference-out docs/reference -check
	go run ./tools/linkcheck

demo:
	go run ./tools/demo

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

verify-reproducible:
	sh scripts/verify-reproducible.sh
