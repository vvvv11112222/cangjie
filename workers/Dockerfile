FROM golang:1.26.0-bookworm AS audio-planner
WORKDIR /planner
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/audio-plan/ ./cmd/audio-plan/
RUN CGO_ENABLED=0 go build -trimpath -o /out/audio-plan ./cmd/audio-plan

FROM python:3.12.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends libgl1 libglib2.0-0 libgomp1 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY workers/requirements.lock /app/workers/requirements.lock
RUN python -m pip install --no-cache-dir -r workers/requirements.lock
COPY contracts/v1.schema.json /app/contracts/v1.schema.json
COPY workers /app/workers
COPY --from=audio-planner /out/audio-plan /app/bin/audio-plan
ENV ASR_PLANNER=/app/bin/audio-plan
ENV PYTHONUNBUFFERED=1 VISION_RUNTIME=cpu WORKER_CAPABILITIES=probe,video_analysis
CMD ["python", "-m", "workers", "run"]
