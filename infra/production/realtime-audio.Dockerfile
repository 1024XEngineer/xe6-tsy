FROM golang:1.26.7-bookworm@sha256:659cc38c1a394eeb4dd7e31fff6df128bd33444dcc7afd70e3bed5225749dbc0 AS build

WORKDIR /src
COPY . .

ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go -C services/realtime-audio build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/lingow-realtime-audio .

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241 AS onnxruntime

ARG TARGETARCH=amd64
ARG ONNXRUNTIME_VERSION=1.24.1

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tar \
    && rm -rf /var/lib/apt/lists/* \
    && case "${TARGETARCH}" in \
      amd64) asset="onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" ;; \
      arm64) asset="onnxruntime-linux-aarch64-${ONNXRUNTIME_VERSION}.tgz" ;; \
      *) echo "unsupported ONNX Runtime architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl --fail --location --retry 3 --output /tmp/onnxruntime.tgz "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/${asset}" \
    && mkdir -p /opt/onnxruntime/extract /opt/onnxruntime/runtime \
    && tar -xzf /tmp/onnxruntime.tgz -C /opt/onnxruntime/extract \
    && runtime_dir="$(find /opt/onnxruntime/extract -mindepth 1 -maxdepth 1 -type d | head -n 1)" \
    && test -n "$runtime_dir" \
    && cp -a "$runtime_dir"/. /opt/onnxruntime/runtime/ \
    && versioned_library="$(find /opt/onnxruntime/runtime/lib -maxdepth 1 -type f -name 'libonnxruntime.so.*' | head -n 1)" \
    && test -n "$versioned_library" \
    && ln -sfn "$(basename "$versioned_library")" /opt/onnxruntime/runtime/lib/libonnxruntime.so \
    && rm /tmp/onnxruntime.tgz

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl libgomp1 libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /app lingow

WORKDIR /app
COPY --from=build /out/lingow-realtime-audio /app/lingow-realtime-audio
COPY --from=build /src/services/realtime-audio/vad/silero/silero_vad.onnx /app/vad/silero/silero_vad.onnx
COPY --from=onnxruntime /opt/onnxruntime/runtime/lib /app/third_party/onnxruntime/lib

ENV ONNXRUNTIME_SHARED_LIBRARY_PATH=/app/third_party/onnxruntime/lib/libonnxruntime.so

USER lingow
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=12 CMD ["curl", "--fail", "--silent", "http://127.0.0.1:8090/healthz"]
ENTRYPOINT ["/app/lingow-realtime-audio"]
