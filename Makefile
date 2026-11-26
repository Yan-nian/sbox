.PHONY: all frontend build build-linux-amd64 build-linux-arm64 test vet clean

GO ?= go
NPM ?= npm
VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.Version=$(VERSION)
BINARY := sbox-panel

all: frontend build

frontend:
	cd web && $(NPM) ci && $(NPM) run build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/sbox-panel

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY).exe ./cmd/sbox-panel

build-linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/sbox-panel

build-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/sbox-panel

vet:
	$(GO) vet ./...

test:
	$(GO) test -v -race ./...

test-norace:
	$(GO) test -v ./...

clean:
	rm -rf $(BINARY) $(BINARY).exe dist/
