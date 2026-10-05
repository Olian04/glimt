BIN := glimt

.PHONY: build test race vet update clean release-check snapshot

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

build: ## Build ./glimt.
	go build -o $(BIN) .

test: ## Unit tests and golden snapshots. Every test lives in ./test.
	go test ./...

race: ## Race detector.
	go test -race ./...

vet: ## Vet checker.
	go vet ./...

update: ## Accept an intended visual change, then review: git diff test/testdata/golden
	go test ./test -update

clean: ## Clean up.
	rm -rf $(BIN) dist
	find test -name '*.ansi.got' -delete

release-check: ## Validate .goreleaser.yaml.
	goreleaser check

snapshot: ## Build all release binaries into ./dist without publishing.
	goreleaser release --snapshot --clean
