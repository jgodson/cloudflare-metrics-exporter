BINARY ?= cloudflare-metrics-exporter
IMAGE ?= ghcr.io/jasongodson/cloudflare-metrics-exporter:latest

.PHONY: build test run tidy docker-build

build:
	mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/exporter

test:
	go test ./...

run:
	go run ./cmd/exporter

tidy:
	go mod tidy

docker-build:
	docker build -t $(IMAGE) .
