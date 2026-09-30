# qc — developer tasks. Run `make help` for the list.

COVERAGE := coverage.out

# Build metadata printed by qc version (the latest tag, "dev" before the first).
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# The release the Homebrew formula points at (make homebrew): the latest tag.
RELEASE ?= $(shell git describe --tags --abbrev=0 2>/dev/null)
# The local clone of the eko/homebrew-tap repository.
HOMEBREW_TAP ?=

IMAGE ?= qc
IMAGE_CUDA ?= qc:cuda

.PHONY: binary homebrew help build install test race cover cover-html lint fmt vet check nocgo clean docker docker-test docker-cuda gpu-validate

help: ## Show this help
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the qc binary into bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/qc ./cmd/qc

binary: ## Build the self-contained release binary of this platform into dist/ (needs meson, ninja, pkgconf)
	packaging/release/build-binary.sh $(VERSION) $(COMMIT) $(DATE) dist

homebrew: ## Point the Homebrew formula at the latest tag (or RELEASE=vX.Y.Z), commit it in HOMEBREW_TAP; CHECK=1 tests it, PUSH=1 pushes
	@test -n "$(HOMEBREW_TAP)" || { echo "set HOMEBREW_TAP to your clone of eko/homebrew-tap"; exit 1; }
	@test -n "$(RELEASE)" || { echo "no tag: set RELEASE=vX.Y.Z"; exit 1; }
	packaging/homebrew/update.sh $(RELEASE) $(HOMEBREW_TAP)

install: ## Install qc into $GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/qc

test: ## Run the tests
	go test ./...

race: ## Run the tests with the race detector
	go test -race ./...

cover: ## Run the tests with coverage and print the per-function summary
	go test -race -coverprofile=$(COVERAGE) -covermode=atomic ./...
	go tool cover -func=$(COVERAGE) | tail -n 1

cover-html: cover ## Open the coverage report in a browser
	go tool cover -html=$(COVERAGE)

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format the code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

# The service packages must build without cgo: only vmaf/libvmaf binds libvmaf.
nocgo: ## Build the service packages without cgo
	CGO_ENABLED=0 go build ./analysis ./ladder ./nvidia ./pipeline ./quality

check: vet nocgo lint race ## Everything CI runs

docker: ## Build the CPU Docker image (IMAGE=qc)
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE) .

docker-test: docker ## Smoke-test the Docker image on synthetic clips
	packaging/docker/smoke-test.sh $(IMAGE)

docker-cuda: ## Build the NVIDIA image (Dockerfile.cuda, IMAGE_CUDA=qc:cuda)
	docker build -f Dockerfile.cuda --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE_CUDA) .

gpu-validate: docker-cuda ## Run the GPU validation kit on an NVIDIA machine (report in gpu-validation/)
	QC_IMAGE=$(IMAGE_CUDA) QC_GPU_OUT=gpu-validation bench/gpu/validate.sh

clean: ## Remove build and coverage artifacts
	rm -rf bin $(COVERAGE)
