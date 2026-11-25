GO_VERSION := go1.27.3
GOFMT := $(shell go env GOROOT)/bin/gofmt
GOCACHE ?= $(abspath ../.cache/go-build)
GOPROXY ?= off
OUTPUT ?= $(abspath ../.scratch/backend-build)
export GOCACHE GOPROXY
export GOTOOLCHAIN := local
.PHONY: toolchain deps format-check vet test race contract build verify
toolchain:
	@test "$$(go env GOVERSION)" = "$(GO_VERSION)" || { echo "Require exact $(GO_VERSION)"; exit 1; }
deps: toolchain
	go mod download
format-check: toolchain
	@test -z "$$("$(GOFMT)" -l .)"
vet: toolchain
	go vet ./...
test: toolchain
	go test -count=1 -timeout=180s ./...
race: toolchain
	go test -race -count=1 -timeout=240s ./...
contract: toolchain
	python3 ../tools/contract/check_contract.py
build: toolchain
	@mkdir -p "$(OUTPUT)"
	go build -buildvcs=false -mod=readonly -trimpath -o "$(OUTPUT)/server" ./cmd/server
verify: format-check vet test contract build
