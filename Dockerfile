# syntax=docker/dockerfile:1

# Use the jecdf image matching the target platform.
FROM ghcr.io/uncertaintea-io/jecdf:latest AS jecdf-stage

# Build the application from source
FROM golang:1.26 AS build-stage
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build ./cmd/weewoo-server

# Deploy the application binary into a lean image
FROM gcr.io/distroless/base-debian11 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/weewoo-server ./weewoo-server
COPY --from=build-stage /app/ui/dist ./ui/dist
COPY --from=jecdf-stage /jecdf ./jecdf

# run it!
EXPOSE 8080
USER nonroot:nonroot
CMD ["./weewoo-server"]
