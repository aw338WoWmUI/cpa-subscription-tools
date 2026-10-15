ID := cpa-subscription-tools
VERSION ?= 0.1.0
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
EXT := so
ifeq ($(GOOS),darwin)
EXT := dylib
endif

.PHONY: test build
test:
	go test -race ./...
	go vet ./...

build:
	mkdir -p dist/$(GOOS)/$(GOARCH)
	CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -ldflags '-s -w -X main.version=$(VERSION)' -o dist/$(GOOS)/$(GOARCH)/$(ID).$(EXT) .
	rm -f dist/$(GOOS)/$(GOARCH)/$(ID).h
