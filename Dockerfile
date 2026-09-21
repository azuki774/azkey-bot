FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /out/azkey-roumu-bot ./cmd/azkey-roumu-bot

FROM gcr.io/distroless/static-debian13:nonroot AS runtime

COPY --from=build --chown=65532:65532 /out/azkey-roumu-bot /usr/local/bin/azkey-roumu-bot

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/azkey-roumu-bot"]
