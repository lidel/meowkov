# meowkov
[Markov chain](https://en.wikipedia.org/wiki/Markov_chain) IRC bot (PoC exercise in [golang](http://golang.org/) and [Redis](http://redis.io/))

## Quick Start

To start your own instance:

1. Clone the repo: `git clone https://github.com/lidel/meowkov.git`
2. Copy `meowkov.conf.template` to `meowkov.conf` and change `BotName` and `RoomName`
3. Run `make docker-rebuild` to buid (in foreground) and run (in background) via Docker container
4. That is all: meowkov bot will join specified room after a few seconds

## Docker Commands

- `make docker-rebuild` builds the app and runs it in a container
- `make docker-stop` stops already existing container
- `make docker-start` starts already existing container
- `make docker-logs` tails the output (runs in debug by default)
- `make docker-clean` removes meowkov containers and a custom image

## Populating the Corpus

The bot is as good as its corpus.
Running it with empty one will not produce any meaningful results for a long time.    
It is a good idea to bootstrap the corpus using old IRC logs, news articles, etc.

Text can be loaded into the corpus (which is backed by Redis) like this:
```
echo "line one\nline two with more text" | make docker-corpus-import
```

One may also want to generate input on a different machine, for example from weechat logs:

```
find ~/.weechat/logs -name "*#foo*" -type f -exec sh -c "grep -vP '^.+\t(</-|-->|--|.*\*)\t' {} | cut -f3 -d$'\t'" \;   > corpus.txt 
```
Then transfer the file to the box with meowkov and perform import there:
```
cat corpus.txt | make docker-corpus-import
```

Changes are instantaneous: corpus import can be performed while bot is running, no restart is required.    
The same text can be imported multiple times: Markov chains are kept in Redis Sets which provide automatic deduplication.
