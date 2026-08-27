FROM golang:1.26.7-bookworm@sha256:659cc38c1a394eeb4dd7e31fff6df128bd33444dcc7afd70e3bed5225749dbc0 AS build

WORKDIR /src
COPY . .

ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go -C services/api build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/lingow-api .

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /app lingow

WORKDIR /app
COPY --from=build /out/lingow-api /app/lingow-api

USER lingow
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=12 CMD ["curl", "--fail", "--silent", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/app/lingow-api"]
