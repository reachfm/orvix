.PHONY: build clean test lint run dev docker

BINARY=orvix
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "1.0.0")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X github.com/orvix/orvix/internal/config.buildVersion=$(VERSION) -X github.com/orvix/orvix/internal/config.buildTime=$(BUILD_TIME)"

all: build

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/orvix/

clean:
	rm -rf $(BUILD_DIR)/
	rm -f coverage.out

test:
	go test ./... -v -cover -coverprofile=coverage.out

lint:
	go vet ./...

run: build
	./$(BUILD_DIR)/$(BINARY)

dev:
	go run ./cmd/orvix/

docker:
	docker build -t orvix:latest .

mod:
	go mod tidy
	go mod verify

.PHONY: webmail admin
webmail:
	cd web/webmail && npm install && npm run build

admin:
	cd web/admin && npm install && npm run build

install:
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/orvix
	mkdir -p /etc/orvix /var/lib/orvix /var/log/orvix
	cp orvix.yaml /etc/orvix/orvix.yaml
