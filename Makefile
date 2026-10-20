VERSION ?= $(shell cat VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || echo -dirty)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install uninstall test vet mock demo notices clean

build: ## Build ./paygate
	go build -ldflags "$(LDFLAGS)" -o paygate ./cmd/paygate

install: ## Install paygate into ~/.local/bin and add it to PATH
	./scripts/install.sh

uninstall:
	./scripts/install.sh --uninstall

test: ## Run all tests with the race detector
	go test -race ./...

vet:
	go vet ./...

mock: ## Run the local mock Paygate on 127.0.0.1:8089
	go run ./tools/mockpaygate

demo: build ## Record the README GIFs (needs vhs and a running mock)
	vhs docs/demo-app.tape
	vhs docs/demo-app-2.tape
	vhs docs/demo.tape
	vhs docs/demo-hosted.tape

notices: ## Regenerate THIRD_PARTY_NOTICES.md
	./scripts/notices.sh

clean:
	rm -f paygate
