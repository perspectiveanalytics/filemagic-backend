.PHONY: build run dev

BINARY=bin/filemagic-server

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w -buildid=" -trimpath -o $(BINARY) ./cmd/server

run: build
	$(BINARY)

dev:
	go run ./cmd/server
