BINARY        := pi-streamer
CMD_DIR       := ./cmd/pi-streamer
INDEXER_BIN   := search-indexer
INDEXER_DIR   := ./cmd/search-indexer
BIN_DIR       := bin
WEB_DIR       := web

# Raspberry Pi Zero 2W target — override PI_ARCH=arm64 if running 64-bit Raspberry Pi OS.
PI_ARCH    ?= arm
GOARM      ?= 6
PI_HOST    ?= pi@raspberrypi.local
PI_PATH    ?= ~/pi-streamer

.PHONY: all build run dev test fmt vet lint clean \
	build-pi build-pi64 build-all deploy-pi deploy-pi64 \
	build-indexer run-indexer \
	web-install web-build web-test \
	install-deps help

all: build

## Common tasks (architecture-independent)

# Debian/Ubuntu dev machine setup: Go toolchain + mpd/mpc for local testing.
install-deps:
	sudo apt-get update
	sudo apt-get install -y golang-go mpd mpc

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet

clean:
	rm -rf $(BIN_DIR)

## x86 (local dev machine)

build:
	go build -o $(BIN_DIR)/$(BINARY) $(CMD_DIR)

run: build
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; ./$(BIN_DIR)/$(BINARY)

# Brings up search-indexer + daemon + frontend dev server together in one
# foreground command; Ctrl+C stops all three. Extra args pass through to the
# daemon, e.g. `make dev ARGS="-oled-port /dev/ttyACM0"`. Assumes mpd itself
# is already running.
dev:
	@./scripts/dev.sh $(ARGS)

## Raspberry Pi Zero 2W (ARM)

build-pi:
	GOOS=linux GOARCH=arm GOARM=$(GOARM) go build -o $(BIN_DIR)/$(BINARY)-arm $(CMD_DIR)

build-pi64:
	GOOS=linux GOARCH=arm64 go build -o $(BIN_DIR)/$(BINARY)-arm64 $(CMD_DIR)

build-all: build build-pi build-pi64

deploy-pi: build-pi
	scp $(BIN_DIR)/$(BINARY)-arm $(PI_HOST):$(PI_PATH)

deploy-pi64: build-pi64
	scp $(BIN_DIR)/$(BINARY)-arm64 $(PI_HOST):$(PI_PATH)

## Search indexer (runs on a separate, more capable machine — not the Pi)

build-indexer:
	go build -o $(BIN_DIR)/$(INDEXER_BIN) $(INDEXER_DIR)

run-indexer: build-indexer
	./$(BIN_DIR)/$(INDEXER_BIN)

## Frontend (React/Vite, in web/)

web-install:
	npm --prefix $(WEB_DIR) install

web-build:
	npm --prefix $(WEB_DIR) run build

web-test:
	npm --prefix $(WEB_DIR) run test --if-present

help:
	@echo "Common:    make install-deps | make test | make fmt | make vet | make lint | make clean"
	@echo "x86:       make build | make run | make dev (indexer+daemon+frontend together, Ctrl+C stops all)"
	@echo "Pi (32-bit armhf):  make build-pi   | make deploy-pi   (PI_HOST=$(PI_HOST))"
	@echo "Pi (64-bit arm64):  make build-pi64 | make deploy-pi64 (PI_HOST=$(PI_HOST))"
	@echo "Indexer:   make build-indexer | make run-indexer"
	@echo "Frontend:  make web-install | make web-build | make web-test"
