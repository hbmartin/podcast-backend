GOCMD=go
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_NAME=podcast-backend
VERSION?=1.0.0
DOCKER_REGISTRY?= #if set it should finished by /

# determine if running on Linux
ifneq ($(OS),Windows_NT)
	GREEN  := $(shell tput -Txterm setaf 2)
	YELLOW := $(shell tput -Txterm setaf 3)
	WHITE  := $(shell tput -Txterm setaf 7)
	CYAN   := $(shell tput -Txterm setaf 6)
	RESET  := $(shell tput -Txterm sgr0)
	GOHOME := $(HOME)/go/bin/
endif

.PHONY: all test build clean coverage lint lint-go vet-go e2e proto sqlc semgrep gosec govulncheck security docker-build docker-run docker-stop docker-release help

all: help

## Run:
run: ## Run the project
	$(GOCMD) run .

## Build:
build: ## Build your project and put the output binary in out/bin/
	$(GOCMD) build -o out/bin/$(BINARY_NAME) .

clean: ## Remove build related file
ifeq ($(OS),Windows_NT)
	del /q /s .\out
	del /q /s .\junit-report.xml
	del /q /s .\junit-raw.txt
	del /q /s .\checkstyle-report.xml
	del /q /s .\coverage.xml
	del /q /s .\profile.json
	del /q /s .\profile.cov
	rmdir /q /s .\out
else
	rm -fr ./bin
	rm -fr ./out
	rm -f ./junit-raw.txt ./junit-report.xml checkstyle-report.xml ./coverage.xml ./profile.json ./profile.cov
endif

## Test:
test: ## Run the tests of the project
	$(GOTEST) -v -race ./...

test-junit: ## Run the tests of the project and export a junit report
	go install github.com/jstemmer/go-junit-report@latest
	$(GOTEST) -v -race 2>&1 ./... > junit-raw.txt
	$(GOHOME)go-junit-report -set-exit-code < junit-raw.txt > junit-report.xml

coverage: ## Run the tests of the project and display coverage
	$(GOTEST) -cover -covermode=count -coverprofile=profile.cov ./...
	$(GOCMD) tool cover -func profile.cov

cobertura: ## Run the tests of the project and export a cobertura coverage xml
	go install github.com/axw/gocov/gocov@latest
	go install github.com/AlekSi/gocov-xml@latest
	$(GOTEST) -cover -covermode=count -coverprofile=profile.cov ./...
	$(GOCMD) tool cover -func profile.cov
	$(GOHOME)gocov convert profile.cov > profile.json
	$(GOHOME)gocov-xml < profile.json > coverage.xml

## Codegen:
PROTOC_VERSION=35.0

proto: ## Regenerate Go protobuf code from protos/api.proto (requires protoc $(PROTOC_VERSION))
	@actual="$$(protoc --version 2>/dev/null || true)"; \
		[ "$$actual" = "libprotoc $(PROTOC_VERSION)" ] || { \
			echo "protoc $(PROTOC_VERSION) required, found: $${actual:-not installed}"; \
			exit 1; \
		}
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	PATH="$$PATH:$(GOHOME)" protoc -I protos --go_out=. --go_opt=module=github.com/hbmartin/podcast-backend protos/api.proto

sqlc: ## Regenerate database code from db/queries.sql
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
	$(GOHOME)sqlc generate

e2e: ## Run the end-to-end suite (needs Postgres; see readme)
	go test -tags e2e -count=1 -v ./e2e

## Lint:
lint: vet-go lint-go ## Run all available linters

lint-go: ## Use staticcheck on your project
	go install honnef.co/go/tools/cmd/staticcheck@latest
	$(GOHOME)staticcheck ./...

vet-go: ## Use go vet on your project
	$(GOVET)

semgrep: ## Run repository-specific security and correctness rules
	semgrep --config .semgrep.yml --error .

gosec: ## Check for disabled TLS verification (gosec G402 only; broader rules live in .semgrep.yml)
	$(GOCMD) run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 -exclude-generated -include=G402 ./...

govulncheck: ## Check reachable Go dependencies for known vulnerabilities
	$(GOCMD) run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

security: semgrep gosec govulncheck ## Run all security prevention checks

## Docker:
docker-build: ## Use the dockerfile to build the container
	docker build --rm --tag $(BINARY_NAME) .

docker-run: ## Use docker compose to run the project
	docker compose up --detached

docker-stop: ## Use docker compose to stop the running project
	docker compose down

docker-release: ## Release the container with tag latest and version
	docker tag $(BINARY_NAME) $(DOCKER_REGISTRY)$(BINARY_NAME):latest
	docker tag $(BINARY_NAME) $(DOCKER_REGISTRY)$(BINARY_NAME):$(VERSION)
	docker push $(DOCKER_REGISTRY)$(BINARY_NAME):latest
	docker push $(DOCKER_REGISTRY)$(BINARY_NAME):$(VERSION)

## Help:
help: ## Show this help.
ifeq ($(OS),Windows_NT)
	@echo Usage:
	@echo make target
else
	@echo ''
	@echo 'Usage:'
	@echo '${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} { \
		if (/^[a-zA-Z_-]+:.*?##.*$$/) {printf "    ${YELLOW}%-20s${GREEN}%s${RESET}\n", $$1, $$2} \
		else if (/^## .*$$/) {printf "  ${CYAN}%s${RESET}\n", substr($$1,4)} \
		}' $(MAKEFILE_LIST)
endif
