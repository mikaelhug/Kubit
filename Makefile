BIN     := bin/kubit
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test web clean tofu-lock

build: web
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/kubit

web: web/node_modules/.package-lock.json
	cd web && npm run build

web/node_modules/.package-lock.json: web/package-lock.json
	cd web && npm ci
	@touch $@

test:
	go test ./...

tofu-lock:
	hack/tofu-lock.sh

clean:
	rm -rf bin web/dist
