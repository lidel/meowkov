GO ?= go
GOLINT ?= golint
GOPATH := $(CURDIR)/_vendor:$(GOPATH)
GITHASH="-X main.version $(shell git rev-parse --short HEAD)"

all: lint build

build:
	$(GO) build -ldflags $(GITHASH)  meowkov.go
lint:
	$(GOLINT)

run:
	./meowkov
