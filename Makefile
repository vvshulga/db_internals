.PHONY: build build-all test clean help

## build: Compile all Go binaries (no npm required)
build:
	go build -o db_internals .
	go build -o storage_cli   ./cmd/storage_cli/
	go build -o dbserver      ./cmd/dbserver/
	go build -o dbctl         ./cmd/dbctl/
	go build -o seeddb        ./cmd/seeddb/
	cd ui_admin/backend && go build -o ../ui_admin_server . && cd ../..

## build-all: Compile everything including the web admin (requires Node.js + npm)
build-all: build
	$(MAKE) -C ui_admin all

## test: Run all tests with race detector
test:
	go test -race ./...

## clean: Remove all build artifacts
clean:
	rm -f db_internals storage_cli dbserver dbctl seeddb ui_admin_server
	$(MAKE) -C ui_admin clean

## help: List available targets
help:
	@grep -E '^## ' Makefile | sed 's/## //'
