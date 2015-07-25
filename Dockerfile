FROM golang:latest

LABEL meowkov=yes

RUN mkdir /meowkov
COPY Makefile meowkov.go /meowkov/
COPY .git /meowkov/.git
WORKDIR /meowkov
RUN make build
RUN rm -rf /meowkov/.git /meowkov/Makefile

CMD ["/meowkov/meowkov"]
