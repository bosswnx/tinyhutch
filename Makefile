VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test vet dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyfm ./src

test:
	go test ./...

vet:
	go vet ./...

dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyfm-linux-amd64 ./src
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyfm-linux-arm64 ./src
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/tinyfm-darwin-arm64 ./src
	shasum -a 256 dist/tinyfm-* > dist/SHA256SUMS

clean:
	rm -rf dist
