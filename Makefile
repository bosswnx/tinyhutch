VERSION ?= 0.3.1
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test vet dist snapshot clean docker-build docker-up docker-down

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyhutch ./src

test:
	go test ./...

vet:
	go vet ./...

dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyhutch-linux-amd64 ./src
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyhutch-linux-arm64 ./src
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyhutch-darwin-amd64 ./src
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyhutch-darwin-arm64 ./src
	shasum -a 256 dist/tinyhutch-* > dist/SHA256SUMS

# Local GoReleaser dry run: builds all targets into .goreleaser/ without
# publishing anything. Requires goreleaser v2.
snapshot:
	goreleaser release --snapshot --clean

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t tinyhutch:$(VERSION) .

# One-click deployment: build the image and start the container in the
# background. Data and config live in ./data.
docker-up:
	TINYHUTCH_VERSION=$(VERSION) docker compose up -d --build

docker-down:
	docker compose down

clean:
	rm -rf dist .goreleaser
