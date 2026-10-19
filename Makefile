VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test lint tidy clean

build: ## Build ./dockerterm
	go build -trimpath -ldflags "$(LDFLAGS)" -o dockerterm .

run: ## Run with the debug log enabled
	go run . -debug

test: ## Run tests with the race detector
	go test -race ./...

lint: ## Run golangci-lint (https://golangci-lint.run/welcome/install/)
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -rf dockerterm dist
