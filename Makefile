# Common development tasks. Everything also works with plain `go` commands.
VERSION ?= 0.1.0-dev

.PHONY: all build test race acceptance kiwix package fmt vet clean-dist

all: vet test build

build:
	go build -o arcana ./cmd/arcana

kiwix:
	scripts/fetch-kiwix.sh

test: kiwix
	go test -count=1 ./...

race: kiwix
	go test -race -count=1 ./...

# Runs the offline acceptance test in an isolated network namespace (Linux).
acceptance: kiwix
	go test -tags acceptance -count=1 -v ./test/acceptance

package:
	scripts/package.sh $(VERSION)

fmt:
	gofmt -w .

vet:
	go vet ./...
	go vet -tags acceptance ./test/acceptance
