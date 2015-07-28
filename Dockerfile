FROM golang:latest

LABEL meowkov=yes

RUN groupadd -g 1042 meowkov && \
    useradd -r -u 1042 -g meowkov -s /sbin/nologin meowkov && \
    mkdir /meowkov
COPY Makefile meowkov.go /meowkov/
COPY .git /meowkov/.git
RUN  chown -R meowkov:meowkov /meowkov
WORKDIR /meowkov

USER meowkov

RUN make build && \
    rm -rf /meowkov/Makefile /meowkov/.git

ENTRYPOINT ["/meowkov/meowkov"]
