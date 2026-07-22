.PHONY: build build-frontend build-backend run test clean fmt lint dev docker-build docker-run release

BINARY=orvix
MODULE=github.com/orvixemail/orvix
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "development")
BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CHANNEL=nightly

LDFLAGS=-ldflags="-s -w \
	-X main.Version=$(VERSION) \
	-X main.Product=OrvixEM \
	-X main.Commit=$(COMMIT) \
	-X main.Channel=$(CHANNEL) \
	-X main.BuildDate=$(BUILD_DATE)"

# Build all: frontend + backend
all: fmt vet build-frontend build-backend

# Backend build (Linux amd64)
build-backend:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY) ./cmd/orvix
	@if command -v file >/dev/null 2>&1; then \
		file $(BINARY) | grep -q "ELF" && echo "  ✅ ELF binary" || echo "  ❌ NOT ELF"; \
	fi

# Frontend builds
build-frontend:
	cd web/admin && npm install && npx vite build --config vite.config.ts
	cd web/webmail && npm install && npx vite build --config vite.config.ts
	cd web/portal && npm install && npx vite build --config vite.config.ts

# Combined build
build: build-frontend build-backend
	@echo "OrvixEM v$(VERSION) built"
	@echo "  Binary: $(BINARY)"
	@ls -lh $(BINARY)

run:
	./$(BINARY) start

test:
	go test ./... -v -count=1

test-short:
	go test ./... -short -count=1

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

tidy:
	go mod tidy
	go mod verify

clean:
	rm -f $(BINARY)
	rm -f *.db *.db-shm *.db-wal
	rm -rf tmp/
	rm -rf web/admin/dist web/webmail/dist web/portal/dist
	rm -rf dist/

dev: build-backend
	./$(BINARY)

# Install locally
install:
	mkdir -p /etc/orvix /var/lib/orvix/{rollback,snapshots,data} /var/log/orvix
	cp $(BINARY) /usr/local/bin/
	[ -f /etc/orvix/orvix.yaml ] || cp configs/orvix.yaml /etc/orvix/

# Docker
docker-build:
	docker build -t orvixemail/orvix:$(VERSION) .
	docker build -t orvixemail/orvix:latest .

docker-run:
	docker-compose up -d

# Release
release: test build
	@echo "==> OrvixEM v$(VERSION) release ready"
	@echo "    Binary: $(BINARY)"
	@echo "    Frontend: admin/webmail/portal"
	@sha256sum $(BINARY) > SHA256SUMS 2>/dev/null || true
	@mkdir -p dist
	@cp $(BINARY) dist/
	@cp configs/orvix.yaml dist/
	@echo "    Artifacts in dist/"

release-all: build
	mkdir -p dist
	for PLATFORM in linux/amd64 linux/arm64 darwin/amd64; do \
		GOOS=$${PLATFORM%/*}; \
		GOARCH=$${PLATFORM#*/}; \
		OUTPUT="orvix-$${GOOS}-$${GOARCH}-v$(VERSION)"; \
		echo "  Building for $$GOOS/$$GOARCH..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH \
			go build $(LDFLAGS) -o "dist/$$OUTPUT" ./cmd/orvix; \
	done
	@echo "Multi-platform builds in dist/"
