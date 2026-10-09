# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kollekt ./cmd/kollekt

FROM alpine:3.20
LABEL org.opencontainers.image.source="https://github.com/yniverz/kollekt" org.opencontainers.image.licenses="AGPL-3.0-only"
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 kollekt && mkdir /data && chown kollekt /data
COPY --from=build /out/kollekt /usr/local/bin/kollekt
USER kollekt
ENV KOLLEKT_DATA=/data KOLLEKT_ADDR=:8080 TZ=Europe/Berlin
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=4s --start-period=10s CMD ["kollekt", "healthcheck"]
ENTRYPOINT ["kollekt"]
