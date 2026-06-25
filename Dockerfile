# syntax=docker/dockerfile:1

# Build the application from source
FROM golang:1.26 AS build-stage
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o ./weewoo ./cmd/weewoo-server

# Deploy the application binary into a lean image
FROM gcr.io/distroless/base-debian11 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/weewoo ./weewoo
COPY --from=build-stage /app/ui/dist ./ui/dist

# run it!
EXPOSE 8080
USER nonroot:nonroot
CMD ["./weewoo"]
