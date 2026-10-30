.PHONY: default help build build-release test test-race coverage go-lint go-vet govulncheck security go-update go-clean install sonar sonar-up sonar-down sonar-scan sonar-query

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOLANGCI_LINT_VERSION ?= v2.15.0
LDFLAGS_VERSION = -X github.com/julienhmmt/dockerdownloader/pkg/version.Version=$(VERSION)

default: help

help:
	@echo "Available targets:"
	@echo "  build          Build the dockerdownloader binary"
	@echo "  build-release  Build optimized release binary"
	@echo "  test           Run all tests"
	@echo "  test-race      Run tests with race detector"
	@echo "  coverage       Run tests with coverage profile (coverage.out)"
	@echo "  go-lint        Run golangci-lint on the codebase"
	@echo "  go-vet         Run go vet"
	@echo "  govulncheck    Scan for known vulnerabilities (CVEs)"
	@echo "  security       Run vet + lint + govulncheck"
	@echo "  go-update      Update Go module dependencies"
	@echo "  go-clean       Remove build artifacts and caches"
	@echo "  install        Install the binary to \$$GOPATH/bin"
	@echo "  sonar          Coverage + scan into the shared SonarQube"
	@echo "  sonar-query    Query SonarQube (CMD=\"summary\")"

build:
	go build -ldflags "$(LDFLAGS_VERSION)" -o dockerdownloader .

build-release:
	go build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o dockerdownloader .

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

coverage:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -n 1

go-lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

go-vet:
	go vet ./...

govulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

security: go-vet go-lint govulncheck

go-update:
	go get -u ./...
	go mod tidy
	go mod verify

go-clean:
	rm -f dockerdownloader
	go clean -cache

install:
	go install .

# SonarQube (shared local server, see $(SONAR_HOME)/README.md)
SONAR_HOME ?= $(HOME)/git/gh/sonarqube

sonar: sonar-scan

sonar-up:
	@$(MAKE) -C $(SONAR_HOME) up

sonar-down:
	@$(MAKE) -C $(SONAR_HOME) down

sonar-scan:
	@tools/sonar-coverage.sh
	@$(SONAR_HOME)/scan.sh

sonar-query:
	@python3 $(SONAR_HOME)/query.py $(CMD)
