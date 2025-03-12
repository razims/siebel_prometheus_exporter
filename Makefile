.PHONY: all clean build build-linux build-windows test

VERSION ?= $(shell git describe --tags --always || echo "dev")
BUILD_TIME = $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS = -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -s -w"
BINARY_NAME = siebel_exporter

all: clean build

clean:
	@echo "Cleaning..."
	@rm -rf dist/
	@mkdir -p dist/

test:
	@echo "Running tests..."
	@go test ./...

build: build-linux build-windows

build-linux:
	@echo "Building for Linux AMD64..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)_linux_amd64 ./cli
	@cd dist && tar -czvf $(BINARY_NAME)_$(VERSION)_linux_amd64.tar.gz $(BINARY_NAME)_linux_amd64
	@cd dist && sha256sum $(BINARY_NAME)_$(VERSION)_linux_amd64.tar.gz > $(BINARY_NAME)_$(VERSION)_linux_amd64.tar.gz.sha256

build-windows:
	@echo "Building for Windows AMD64..."
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)_windows_amd64.exe ./cli
	@cd dist && zip $(BINARY_NAME)_$(VERSION)_windows_amd64.zip $(BINARY_NAME)_windows_amd64.exe
	@cd dist && sha256sum $(BINARY_NAME)_$(VERSION)_windows_amd64.zip > $(BINARY_NAME)_$(VERSION)_windows_amd64.zip.sha256

release: clean test build
	@echo "Creating release artifacts..."
	@cd dist && sha256sum $(BINARY_NAME)_* > checksums.txt
	@echo "Done! Release artifacts are in the dist/ directory"