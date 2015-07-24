GO ?= go
GOLINT ?= golint
GOPATH := $(CURDIR)/_vendor:$(GOPATH)
GITHASH="-X main.version $(shell git rev-parse --short HEAD)"
DEPS=$(shell go list -f '{{ join .Deps  "\n"}}' . | grep github.com)

all: lint build

build: deps
	$(GO) build -ldflags $(GITHASH)  meowkov.go
lint:
	@$(GOLINT)
deps:
	 @echo $(DEPS) | xargs -n1 go get -v
updatedeps:
	 @echo $(DEPS) | xargs -n1 go get -v -u
run:
	./meowkov
