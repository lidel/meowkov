# meowkov

![meowkov is happy to interact with humans](https://i.imgur.com/Hja2KRJm.jpg)

[![Build Status](https://travis-ci.org/lidel/meowkov.svg)](https://travis-ci.org/lidel/meowkov)
[![Coverage Status](https://coveralls.io/repos/lidel/meowkov/badge.svg?branch=master&service=github)](https://coveralls.io/github/lidel/meowkov?branch=master)
[![Go Report Card](http://goreportcard.com/badge/lidel/meowkov)](http://goreportcard.com/report/lidel/meowkov)

> [Markov chain](https://en.wikipedia.org/wiki/Markov_chain) IRC bot and PoC exercise in [golang](http://golang.org/) and [Redis](http://redis.io/)

## Table of Contents

- [Background](#background)
  - [Markov Chains](#markov-chains)
- [Usage](#usage)
  - [Standalone](#running-standalone-binary)
  - [Docker](#running-with-docker)
  - [Populating Corpus](#populating-corpus)
- [License](#license)

## Background

Meowkov is a simple [Markov chain](https://en.wikipedia.org/wiki/Markov_chain) IRC bot.
Created as a learning exercise in [golang](http://golang.org/) and [Redis](http://redis.io/).

### Markov Chains

- [Markov Chains at Wikipedia](https://en.wikipedia.org/wiki/Markov_chain)
- [Markov Chain Explained Visually](http://setosa.io/ev/markov-chains/)


## Usage

### Running Standalone Binary

To start your own instance:

1. Clone the repo: `git clone https://github.com/lidel/meowkov.git`
2. Copy `meowkov.conf.template` to `meowkov.conf` and change at least `BotName`, `Channels` and `RedisServer`
3. Run `make build` to build `meowkov` binary
4. Run `./meowkov`
5. That is all: meowkov bot will join specified room after a few seconds

#### Commands

- `make build` builds the app
- `make dev-updatedeps` updates dependencies to latest versions
- `./meowkov` runs the app against `meowkov.conf` in current directory
- `./meowkov -c /some/path/meowkov.conf` runs the app with specified config file
- `echo "some text" | ./meowkov -import=true -purge=false`  adds piped strings to the corpus
- `echo "some text" | ./meowkov -import=true -purge=true` replaces corpus with piped data
  (destructive, remember to backup Redis database before executing this)

### Running with Docker

To start dockerized instance with latest Redis:

1. Clone the repo: `git clone https://github.com/lidel/meowkov.git`
2. Copy `meowkov.conf.template` to `meowkov.conf` and change at least `BotName` and `Channels` (`RedisServer` will be set automatically by Docker)
3. Run `make docker-rebuild` to build (in foreground) and run (in background) via Docker container
4. That is all: meowkov bot will join specified room after a few seconds

#### Docker Commands

`Makefile` provides commands that simplify build and deployment.    
Commands assume `meowkov.conf` to be present in the same directory as `Makefile`.    
Redis data with corpus is persisted to `data/dump.rpd`.

- `make docker-rebuild` builds the app and runs it in a container
- `make docker-update` same as `docker-rebuild` but also checks for updates of `golang` and `redis` images
- `make docker-stop` stops already existing container
- `make docker-start` starts already existing container
- `make docker-logs` tails the output (runs in debug by default)
- `make docker-clean` removes meowkov containers and a custom image
- `echo "some text" | make docker-corpus-add` adds piped strings to the corpus
- `echo "some text" | make docker-corpus-replace` replaces corpus with piped data
  (destructive, remember to backup `data/dump.rpd` before executing this)

### Populating Corpus

The bot is as good as its corpus.    
Running it with empty one will not produce any meaningful results for a long time.    
It is a good idea to bootstrap the corpus using old IRC logs, news articles, etc.

Text can be loaded into the corpus (which is backed by Redis) like this:
```
echo "line one\nline two with more text" | make docker-corpus-add
```
or if run in standalone mode:
```
echo "line one\nline two with more text" | ./meowkov -import=true -purge=false
```

One may also want to generate input on a different machine, for example from weechat logs:

```
find ~/.weechat/logs -name "*#foo*" -type f -exec sh -c "grep -vP '^.+\t(</-|-->|--|.*\*)\t' {} | cut -f3 -d$'\t'" \; > corpus.txt
```
Then transfer the file to the box with meowkov and perform import there:
```
cat corpus.txt | make docker-corpus-add
```

Changes are instantaneous: corpus import can be performed while bot is running, no restart is required.    
The same text can be imported multiple times: Markov chains are kept in Redis Sets which provide automatic deduplication.

## License

[CC0 Public Domain Dedication](https://creativecommons.org/publicdomain/zero/1.0/)
