MODULE := github.com/sebastianhutter/local-rag-go
BINARY := local-rag
BUILD_DIR := bin
CGO_ENABLED := 1
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

# Build tags needed for FTS5 support in go-sqlite3
BUILD_TAGS := sqlite_fts5

.PHONY: all build test lint clean app dmg

all: build

build:
	CGO_ENABLED=$(CGO_ENABLED) go build -tags "$(BUILD_TAGS)" -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/local-rag

test:
	CGO_ENABLED=$(CGO_ENABLED) go test -tags "$(BUILD_TAGS)" -race -count=1 ./...

test-v:
	CGO_ENABLED=$(CGO_ENABLED) go test -tags "$(BUILD_TAGS)" -race -count=1 -v ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)

tidy:
	go mod tidy

# macOS .app bundle
app: build
	@echo "Building macOS .app bundle..."
	./scripts/build-app.sh

# macOS DMG installer
dmg: app
	@echo "Building DMG installer..."
	./scripts/build-dmg.sh
