FROM golang:1.26.0-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 go build -trimpath -o /out/bootstrap-admin ./cmd/bootstrap-admin \
    && CGO_ENABLED=0 go build -trimpath -o /out/seed-dev ./cmd/seed-dev

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app
WORKDIR /app
COPY --from=build /out/ /app/bin/
COPY database/ /app/database/
RUN mkdir -p /data/media /data/worker-tmp /data/governance \
    && chown -R app:app /data
USER app
EXPOSE 8080
CMD ["/app/bin/api"]
