# syntax=docker/dockerfile:1

# Use the jecdf image matching the target platform.
FROM ghcr.io/uncertaintea-io/jecdf:latest AS jecdf-stage

# Build the application from source.
FROM golang:1.26 AS build-stage
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/weewoo-server ./cmd/weewoo-server
RUN mkdir -p /out/data

# Assemble the server image used as the source for public releases.
FROM gcr.io/distroless/static-debian12:nonroot AS release-stage

LABEL org.opencontainers.image.licenses="PolyForm-Internal-Use-1.0.0"
LABEL org.opencontainers.image.vendor="Uncertain Tea, Inc."

WORKDIR /app
COPY --from=build-stage --chown=nonroot:nonroot /out/weewoo-server ./weewoo-server
COPY --from=build-stage --chown=nonroot:nonroot /app/ui/dist ./ui/dist
COPY --from=build-stage --chown=nonroot:nonroot /out/data /var/lib/weewoo
COPY --from=build-stage --chown=nonroot:nonroot /app/demo/config.yaml ./config.yaml
COPY --from=build-stage --chown=nonroot:nonroot /app/LICENSES ./licenses
COPY --from=build-stage --chown=nonroot:nonroot /app/NOTICE.md ./licenses/NOTICE.md
COPY --from=jecdf-stage --chown=nonroot:nonroot /jecdf ./jecdf

EXPOSE 8080 5000
VOLUME ["/var/lib/weewoo"]
USER nonroot:nonroot
ENTRYPOINT ["./weewoo-server"]
