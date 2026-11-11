# Docker-wrapped Go toolchain: no host Go install required.
# Keep GO_IMAGE in step with the go directive in go.mod and the release workflow.
GO_IMAGE ?= golang:1.26.3
VERSION  ?= dev
MODULE   := github.com/Krathos-systems/aegis-mcp

# Module and build caches live in .gocache/ (git-ignored) so they survive runs,
# and files are written as the calling user rather than root.
DOCKER_RUN = docker run --rm -u $$(id -u):$$(id -g) \
	-e HOME=/tmp -e GOPATH=/src/.gocache/gopath -e GOCACHE=/src/.gocache/build \
	-e GOFLAGS=-modcacherw \
	-v "$(CURDIR)":/src -w /src $(GO_IMAGE)

LDFLAGS = -s -w -X main.version=$(VERSION)
TARGETS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: tidy fmt fmt-fix vet test cover cover-html check build dist test-install image clean

## tidy: resolve dependencies and write go.sum
tidy:
	$(DOCKER_RUN) go mod tidy

## fmt: list files that are not gofmt-clean (empty output = clean)
fmt:
	@out="$$($(DOCKER_RUN) sh -c 'gofmt -l *.go internal')"; \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

## fmt-fix: rewrite files in place with gofmt
fmt-fix:
	$(DOCKER_RUN) sh -c 'gofmt -w *.go internal'

## vet: run go vet across all packages
vet:
	$(DOCKER_RUN) go vet ./...

## test: run the unit tests (TEST=<regex> to run some, e.g. make test TEST=TestCleanPath)
test:
	$(DOCKER_RUN) go test ./... $(if $(TEST),-run '$(TEST)')

## cover: race-enabled tests with the 100% coverage gate (scripts/coverage.sh)
cover:
	$(DOCKER_RUN) scripts/coverage.sh

## cover-html: open-able report at .gocache/cover.html (run make cover first)
cover-html:
	$(DOCKER_RUN) go tool cover -html=.gocache/cover.out -o .gocache/cover.html

## check: fmt + vet + cover, what CI runs
check: fmt vet cover

## build: static binary for linux/amd64 -> ./aegis-mcp
build:
	$(DOCKER_RUN) sh -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o aegis-mcp .'

## dist: every release target -> dist/, plus SHA256SUMS
dist:
	rm -rf dist && mkdir -p dist
	$(DOCKER_RUN) sh -c 'set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
		echo "building aegis-mcp-$$os-$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -buildvcs=false \
			-ldflags "$(LDFLAGS)" -o dist/aegis-mcp-$$os-$$arch$$ext .; \
	done'
	cd dist && sha256sum aegis-mcp-* > SHA256SUMS

## test-install: test install.sh against a fake release built from this checkout
test-install: dist
	scripts/test-install.sh dist

## image: build the Docker image as aegis-mcp:dev
image:
	docker build --build-arg VERSION=$(VERSION) -t aegis-mcp:dev .

## clean: remove build output
clean:
	rm -rf aegis-mcp aegis-mcp.exe dist
