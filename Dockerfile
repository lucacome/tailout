# syntax=docker/dockerfile:1.20
FROM golang:1.25.5 AS builder
ARG TARGETARCH

WORKDIR /go/src/github.com/lucacome/tailout

COPY --link go.mod go.sum ./
RUN go mod download

COPY --link . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -a -o tailout .


FROM --platform=$BUILDPLATFORM alpine:3.23 AS certs


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
