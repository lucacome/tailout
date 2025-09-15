# syntax=docker/dockerfile:1.18@sha256:dabfc0969b935b2080555ace70ee69a5261af8a8f1b4df97b9e7fbcf6722eddf
FROM golang:1.25.1@sha256:bb979b278ffb8d31c8b07336fd187ef8fafc8766ebeaece524304483ea137e96 AS builder
ARG TARGETARCH

WORKDIR /go/src/github.com/lucacome/tailout

COPY --link go.mod go.sum ./
RUN go mod download

COPY --link . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -a -o tailout .


FROM --platform=$BUILDPLATFORM alpine:3.22@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1 AS certs


FROM scratch AS base
COPY --from=certs --link /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
USER 1001:1001
ENTRYPOINT [ "/usr/bin/tailout" ]


FROM base AS container
COPY --from=builder /go/src/github.com/lucacome/tailout/tailout /usr/bin/
EXPOSE 3000


FROM base AS goreleaser
ARG TARGETARCH
ARG TARGETVARIANT
ARG TARGETPLATFORM

LABEL org.lucacome.tailout.build.target="${TARGETPLATFORM}"
LABEL org.lucacome.tailout.build.version="goreleaser"

COPY --link dist/tailout_linux_${TARGETARCH}${TARGETVARIANT/v/_}*/tailout /usr/bin/tailout
