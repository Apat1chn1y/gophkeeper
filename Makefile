BINARY_SERVER := bin/server
BINARY_CLIENT := bin/client

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X gophkeeper/internal/client/cli.BuildVersion=$(VERSION) \
           -X gophkeeper/internal/client/cli.BuildDate=$(DATE)

.PHONY: all build build-server build-client test test-coverage lint fmt clean run-server

all: build

build: build-server build-client

build-server:
	go build -o $(BINARY_SERVER) ./cmd/server

build-client:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_CLIENT) ./cmd/client

test:
	go test ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	@echo
	@echo "=== Coverage summary ==="
	go tool cover -func=coverage.out | tail -1
	@echo "HTML report: coverage.html"
	go tool cover -html=coverage.out -o coverage.html

test-cover-each:
	go test -cover ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin coverage.out coverage.html

run-server: build-server
	./$(BINARY_SERVER)


.PHONY: build-all
build-all:
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/client-linux-amd64   ./cmd/client
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/client-linux-arm64   ./cmd/client
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/client-darwin-amd64  ./cmd/client
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/client-darwin-arm64  ./cmd/client
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/client-windows-amd64.exe ./cmd/client
	GOOS=linux   GOARCH=amd64 go build -o dist/server-linux-amd64 ./cmd/server
	GOOS=darwin  GOARCH=amd64 go build -o dist/server-darwin-amd64 ./cmd/server
	GOOS=windows GOARCH=amd64 go build -o dist/server-windows-amd64.exe ./cmd/server