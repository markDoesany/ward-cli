APP ?= ward
GO ?= go
VERSION ?= 0.2.0
COMMIT ?= local
BUILD_DATE ?= unknown
CONFIG_DIR ?= .
STATE_DIR ?= .ward
PROJECT ?= tunover
ENVIRONMENT ?= development

RUN_FLAGS := --config-dir "$(CONFIG_DIR)" --state-dir "$(STATE_DIR)" --project "$(PROJECT)" --env "$(ENVIRONMENT)"

LDFLAGS := -s -w -X github.com/ward/ward/cmd.version=$(VERSION) -X github.com/ward/ward/cmd.commit=$(COMMIT) -X github.com/ward/ward/cmd.buildDate=$(BUILD_DATE)

ifeq ($(OS),Windows_NT)
EXE := .exe
SET_ENV = set GOOS=$(1)&& set GOARCH=$(2)&&
else
EXE :=
SET_ENV = GOOS=$(1) GOARCH=$(2)
endif

.DEFAULT_GOAL := help

.PHONY: help run ui build install build-windows build-linux build-darwin fmt test vet cover tidy check clean

help:
ifeq ($(OS),Windows_NT)
	@cmd /c echo Available targets:
	@cmd /c echo   make run ARGS=version         Run WARD
	@cmd /c echo   make ui                      Open the dashboard
	@cmd /c echo   make build                     Build the local executable
	@cmd /c echo   make test                      Run all tests
	@cmd /c echo   make vet                       Run go vet
	@cmd /c echo   make cover                     Run tests with coverage
	@cmd /c echo   make check                     Run formatting, tests, and vet
	@cmd /c echo   make build-windows             Cross-build Windows amd64
	@cmd /c echo   make build-linux               Cross-build Linux amd64
	@cmd /c echo   make build-darwin              Cross-build macOS amd64
	@cmd /c echo   make clean                     Remove generated binaries and coverage
else
	@echo Available targets:
	@echo   make run ARGS="version"       Run WARD
	@echo   make ui                       Open the dashboard
	@echo   make build                     Build the local executable
	@echo   make test                      Run all tests
	@echo   make vet                       Run go vet
	@echo   make cover                     Run tests with coverage
	@echo   make check                     Run formatting, tests, and vet
	@echo   make build-windows             Cross-build Windows amd64
	@echo   make build-linux               Cross-build Linux amd64
	@echo   make build-darwin              Cross-build macOS amd64
	@echo   make clean                     Remove generated binaries and coverage
endif

run:
	$(GO) run . $(RUN_FLAGS) $(ARGS)

ui:
	$(GO) run . $(RUN_FLAGS)

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(APP)$(EXE) .

install:
	$(GO) install -ldflags "$(LDFLAGS)" .

build-windows:
	$(call SET_ENV,windows,amd64) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(APP)-windows-amd64.exe .

build-linux:
	$(call SET_ENV,linux,amd64) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(APP)-linux-amd64 .

build-darwin:
	$(call SET_ENV,darwin,amd64) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(APP)-darwin-amd64 .

fmt:
	gofmt -w cmd internal main.go

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

tidy:
	$(GO) mod tidy

check: fmt test vet

ifeq ($(OS),Windows_NT)
clean:
	-@cmd.exe /c del /q $(APP).exe $(APP)-windows-amd64.exe $(APP)-linux-amd64 $(APP)-darwin-amd64 coverage.out
else
clean:
	rm -f $(APP) $(APP).exe $(APP)-windows-amd64.exe $(APP)-linux-amd64 $(APP)-darwin-amd64 coverage.out
endif
