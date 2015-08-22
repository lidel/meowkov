FROM golang:latest

LABEL meowkov=yes

RUN  mkdir /meowkov
COPY Makefile meowkov.go .git /meowkov/
RUN  chown -R nobody /meowkov

WORKDIR /meowkov
USER nobody

RUN make build && \
    rm -rf /meowkov/Makefile /meowkov/.git

ENTRYPOINT ["/meowkov/meowkov"]
