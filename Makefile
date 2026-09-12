BIN     := bin/kubit
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test web run clean

build: web
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/kubit

web:
	@if [ -d web/node_modules ]; then cd web && npm run build; fi

test:
	go test ./...

clean:
	rm -rf bin web/dist
