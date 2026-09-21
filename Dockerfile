FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/azkey-roumu-bot ./cmd/azkey-roumu-bot

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 app \
    && adduser -S -D -H -u 65532 -G app app

COPY --from=build --chown=app:app /out/azkey-roumu-bot /usr/local/bin/azkey-roumu-bot

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/azkey-roumu-bot"]
