VERSION ?= 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install test vet mock demo clean

build: ## Build ./paygate
	go build -ldflags "$(LDFLAGS)" -o paygate ./cmd/paygate

install: ## Install paygate into $(go env GOPATH)/bin
	go install -ldflags "$(LDFLAGS)" ./cmd/paygate

test: ## Run all tests with the race detector
	go test -race ./...

vet:
	go vet ./...

mock: ## Run the local mock Paygate on 127.0.0.1:8089
	go run ./tools/mockpaygate

demo: build ## Record the README GIFs (needs vhs and a running mock)
	vhs docs/demo.tape
	vhs docs/demo-hosted.tape

clean:
	rm -f paygate
