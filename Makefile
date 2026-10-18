VERSION ?= 0.2.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test vet dist snapshot clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/hutch ./src

test:
	go test ./...

vet:
	go vet ./...

dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/hutch-linux-amd64 ./src
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/hutch-linux-arm64 ./src
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/hutch-darwin-amd64 ./src
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/hutch-darwin-arm64 ./src
	shasum -a 256 dist/hutch-* > dist/SHA256SUMS

# Local GoReleaser dry run: builds all targets into .goreleaser/ without
# publishing anything. Requires goreleaser v2.
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf dist .goreleaser
