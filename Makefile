# Builds the single self-contained Argos binary (§2.1): the frontend first
# (web/ writes straight into internal/webui/dist for embed.FS to pick up,
# see web/vite.config.ts), then the Go binary that embeds it.
.PHONY: build frontend backend run test test-go test-web fmt lint clean

GOEXE := $(shell go env GOEXE)
BINARY := bin/argos$(GOEXE)

build: frontend backend

frontend:
	cd web && npm install && npm run build

backend:
	go build -o $(BINARY) ./cmd/argos

run: build
	./$(BINARY)

test: test-go test-web

test-go:
	go test ./...

test-web:
	cd web && npm test

fmt:
	gofmt -w .

lint:
	gofmt -l .
	go vet ./...
	cd web && npm run lint

clean:
	rm -rf bin
