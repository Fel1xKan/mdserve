VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/Fel1xKan/mdserve/internal/buildinfo
LDFLAGS := -s -w \
	-X $(PKG).Version=$(VERSION) \
	-X $(PKG).Commit=$(COMMIT) \
	-X $(PKG).Date=$(DATE)

.PHONY: build test vet fmt install run clean snapshot

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o mdserve .

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" .

test:
	go test ./... -race

vet:
	go vet ./...

fmt:
	go fmt ./...

run: build
	./mdserve --root . --no-auth

# 本地验证 release 产物（需要 goreleaser）
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf mdserve dist
