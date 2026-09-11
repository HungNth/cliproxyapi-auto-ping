PLUGIN_NAME ?= auto-ping
VERSION ?= 0.1.0
BUILD_DIR ?= dist
GO_LDFLAGS ?= -s -w -X main.pluginVersion=$(VERSION)

EXT_linux = so
EXT_freebsd = so
EXT_darwin = dylib
EXT_windows = dll
PLUGIN_EXT = $(or $(EXT_$(shell go env GOOS)),so)
PLUGIN_OUTPUT = $(BUILD_DIR)/$(PLUGIN_NAME).$(PLUGIN_EXT)
PLUGIN_HEADER = $(BUILD_DIR)/$(PLUGIN_NAME).h

ifeq ($(OS),Windows_NT)
MKDIR = -mkdir
RMFILE = -cmd /c del /q
RMTREE = -cmd /c rmdir /s /q
HEADER_PATH = $(subst /,\,$(PLUGIN_HEADER))
BUILD_PATH = $(subst /,\,$(BUILD_DIR))
else
MKDIR = -mkdir -p
RMFILE = -rm -f
RMTREE = -rm -rf
HEADER_PATH = $(PLUGIN_HEADER)
BUILD_PATH = $(BUILD_DIR)
endif

.PHONY: build test vet clean

build: export CGO_ENABLED = 1
build:
	$(MKDIR) $(BUILD_PATH)
	go build -trimpath -buildmode=c-shared -ldflags "$(GO_LDFLAGS)" -o $(PLUGIN_OUTPUT) .
	$(RMFILE) "$(HEADER_PATH)"

test vet: export CGO_ENABLED = 0
test:
	go test ./...

vet:
	go vet ./...

clean:
	$(RMTREE) "$(BUILD_PATH)"
