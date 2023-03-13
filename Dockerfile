FROM golang:1.19-alpine AS build

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

RUN go mod download

COPY fsm ./fsm
COPY httpd ./httpd
COPY main.go ./

RUN go build -o dpasswd

FROM alpine
COPY --from=build /app/dpasswd /dpasswd
RUN mkdir /data
CMD exec /dpasswd --id=$NODE_ID --datadir=/data --raft-port=$RAFT_PORT --http-port=$HTTP_PORT
