GO      ?= go
GOLINT  ?= golint
D       ?= docker
GITHASH ?= $(shell git rev-parse --short HEAD)
DEPS    ?= $(shell go list -f '{{ join .Deps  "\n"}}' . | grep github.com)
GOSIMPLE := $(shell command -v gosimple 2> /dev/null)
TMP_DIR  = /tmp/meowkov-build

print-%: ; @echo $*=$($*) # eg. make print-DEPS

all:    dev-deps test lint build
travis: dev-deps test build

build:  dev-deps test
	# build statically-linked binary
	CGO_ENABLED=0 $(GO) build -ldflags "-X main.version=$(GITHASH)"  meowkov.go
test: dev-deps
	$(GO) test
lint:
	@$(GOLINT) .
	@$(GO) vet .
ifndef GOSIMPLE
	$(error "gosimple is not available, please install it via: go get honnef.co/go/tools/cmd/gosimple'")
endif
	@$(GOSIMPLE) .
dev-deps:
	@echo $(DEPS) | xargs -n1 go get -v
dev-updatedeps:
	@echo $(DEPS) | xargs -n1 go get -v -u
dev-run: dev-deps test
	$(GO) run meowkov.go

# dockerized build & container run (including redis)
docker-rebuild: docker-stop docker-clean
	# build binary inside golang image (700MB)
	# and put it in a new, small busybox image (<10MB)
	$(D) build -t meowkov_builder -f Dockerfile.build .
	$(D) run --net=none --rm meowkov_builder | docker build -t meowkov -f Dockerfile.run -
	$(D) rmi -f meowkov_builder

	$(D) run -d --restart unless-stopped -v $(CURDIR)/data:/data:rw --name meowkov_corpus redis
	$(D) run -d --restart unless-stopped -v $(CURDIR)/meowkov.conf:/meowkov.conf:ro --link meowkov_corpus:redis -it --name meowkov_irc meowkov
docker-start:
	$(D) start meowkov_corpus
	$(D) start meowkov_irc
docker-stop:
	$(D) ps -f 'name=meowkov_irc' -q | xargs -r docker stop
	$(D) ps -f 'name=meowkov_corpus' -q | xargs -r docker stop
docker-clean: docker-stop
	$(D) ps -f 'name=meowkov*' -q -a | xargs -r docker rm -f
	$(D) images -f 'label=meowkov' -q | xargs -r docker rmi -f
docker-logs:
	$(D) logs --tail=100 -f meowkov_irc
docker-corpus-add: docker-start
	$(D) run -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -i --rm meowkov -import=true -purge=false
docker-corpus-replace: docker-start
	$(D) run -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -i --rm meowkov -import=true -purge=true
docker-update:
	$(D) pull $(shell awk '/^FROM/ { print $$2; exit }' Dockerfile.build)
	$(D) pull $(shell awk '/^FROM/ { print $$2; exit }' Dockerfile.run)
	$(D) pull redis
	$(MAKE) docker-rebuild
