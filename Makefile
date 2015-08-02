GO      ?= go
GOLINT  ?= golint
D       ?= docker
GOPATH  := $(CURDIR)/_vendor:$(GOPATH)
GITHASH  = "$(shell git rev-parse --short HEAD)"
DEPS     = $(shell go list -f '{{ join .Deps  "\n"}}' . | grep github.com)
LDFLAGS  = "-X main.version $(GITHASH)"


print-%: ; @echo $*=$($*) # eg. make print-DEPS

all: deps test lint build
travis: deps test build

build: deps test
	$(GO) build -ldflags $(LDFLAGS)  meowkov.go
test:
	$(GO) test
lint:
	@$(GOLINT)
deps:
	@echo $(DEPS) | xargs -n1 go get -v
updatedeps:
	@echo $(DEPS) | xargs -n1 go get -v -u
run:
	./meowkov

# dockerized build & container run (including redis)
docker-rebuild: docker-stop docker-clean
	$(D) build -t meowkov .
	$(D) run -d -v $(CURDIR)/data:/data --name meowkov_corpus redis
	$(D) run -d -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -it --name meowkov_irc meowkov
docker-start:
	$(D) start meowkov_corpus
	$(D) start meowkov_irc
docker-stop:
	$(D) ps -f 'name=meowkov*' -q | xargs -r docker stop
docker-clean: docker-stop
	$(D) ps -f 'name=meowkov*' -q -a | xargs -r docker rm
	$(D) images -f 'label=meowkov' -q | xargs -r docker rmi
docker-logs:
	$(D) logs --tail=100 -f meowkov_irc
docker-corpus-add: docker-start
	$(D) run -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -i --rm meowkov -import=true -purge=false
docker-corpus-replace: docker-start
	$(D) run -v $(CURDIR)/meowkov.conf:/meowkov/meowkov.conf:ro --link meowkov_corpus:redis -i --rm meowkov -import=true -purge=true

