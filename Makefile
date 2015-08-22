GO      ?= go
GOLINT  ?= golint
D       ?= docker
GITHASH  = $(shell git rev-parse --short HEAD)
DEPS     = $(shell go list -f '{{ join .Deps  "\n"}}' . | grep github.com)


print-%: ; @echo $*=$($*) # eg. make print-DEPS

all:    dev-deps test lint build
travis: dev-deps test build

build:  dev-deps test
	$(GO) build -ldflags "-X main.version $(GITHASH)"  meowkov.go
test: dev-deps
	$(GO) test
lint:
	@$(GOLINT) .
	@$(GO) vet .
dev-deps:
	@echo $(DEPS) | xargs -n1 go get -v
dev-updatedeps:
	@echo $(DEPS) | xargs -n1 go get -v -u
dev-run: deps
	$(GO) run meowkov.go

# dockerized build & container run (including redis)
docker-rebuild: docker-stop docker-clean
	$(D) build -t meowkov .
	$(D) run -d -v $(CURDIR)/data:/data:rw --name meowkov_corpus redis
	$(D) run -d -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -it --name meowkov_irc meowkov
docker-start:
	$(D) start meowkov_corpus
	$(D) start meowkov_irc
docker-stop:
	$(D) ps -f 'name=meowkov*' -q | xargs -r docker stop
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
	$(D) pull $(shell awk '/^FROM/ { print $$2; exit }' Dockerfile)
	$(D) pull redis
	$(MAKE) docker-rebuild
