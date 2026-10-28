# Local shortcuts; CI runs the same steps (.github/workflows).

.PHONY: build test lint demo snapshot release-check screenshots clean

build:
	go build -trimpath -ldflags "-s -w" -o promtop .

test:
	go vet ./...
	go test -race ./...

lint:
	golangci-lint run ./...

# Synthetic metrics on :8080 to try promtop against.
demo:
	go run ./examples/demo

# All release archives and Linux packages in dist/, without publishing.
snapshot:
	goreleaser release --snapshot --clean

release-check:
	goreleaser check

# README images, rendered by the real UI code from the demo's live metrics.
screenshots:
	./scripts/screenshots.sh

clean:
	rm -rf dist promtop
