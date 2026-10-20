FROM golang:1.26-bookworm AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/demo .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates && rm -rf /var/lib/apt/lists/* && useradd -m -u 10001 demo
WORKDIR /app
COPY --from=build /out/demo /app/demo
RUN mkdir /app/data && chown demo:demo /app/data
USER demo
ENV DEMO_HOST=0.0.0.0 DEMO_PORT=8090 DEMO_DATA_DIR=/app/data FFMPEG_PATH=ffmpeg FFPROBE_PATH=ffprobe
EXPOSE 8090
CMD ["/app/demo"]
