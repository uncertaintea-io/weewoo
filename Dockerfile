# syntax=docker/dockerfile:1
FROM golang:1.26 AS build-stage

# make directory inside the image to store all commands
WORKDIR /app
COPY . .

# build the application
RUN go mod download

# deploy the application binary into a lean image
RUN CGO_ENABLED=0 GOOS=linux go build -o ./weewoo
FROM gcr.io/distroless/base-debian11 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/weewoo ./weewoo
COPY --from=build-stage /app/static ./static

# run it!
EXPOSE 8080
USER nonroot:nonroot
CMD ["./weewoo"]
