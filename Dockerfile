FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure Go SQLite driver, so the binary is static and runs on distroless
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /timerbot ./cmd/timerbot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /timerbot /timerbot
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip \
    DB_PATH=/data/timerbot.db \
    HTTP_ADDR=:8080
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/timerbot", "healthcheck"]
ENTRYPOINT ["/timerbot"]
