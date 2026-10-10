# Convenience targets. Everything here is plain npm/go, so the commands also work without make
# (see CONTRIBUTING.md), including on Windows.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X main.version=$(VERSION:v%=%) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)

.PHONY: all ui build test run dev release docker clean

all: build

ui: ## build the web UI into backend/ui/dist (it is embedded into the binary)
	npm --prefix frontend ci
	npm --prefix frontend run build

build: ui ## build ./repoman for this machine
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ../repoman .

test: ## vet and run the backend test suite
	cd backend && go vet ./... && go test ./...

run: build ## build and run with ./data as the data directory
	./repoman serve

dev: ## hot-reloading UI on :5173, proxying to a backend on :8081 (run `go -C backend run .` alongside)
	npm --prefix frontend run dev

release: ## cross-compile and package every platform into dist/
	./scripts/build-release.sh $(VERSION)

docker: ## build the container image for this machine
	docker build --build-arg VERSION=$(VERSION:v%=%) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(DATE) -t repoman:$(VERSION:v%=%) .

clean:
	rm -rf dist repoman repoman.exe backend/ui/dist
