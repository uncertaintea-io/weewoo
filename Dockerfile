# syntax=docker/dockerfile:1
FROM golang:1.26.3

# make directory inside the image to store all commands
WORKDIR /app

# copies go mod and sum files into the directory
COPY go.mod go.sum ./

# install directories inside the image
RUN go mod download

# install app
COPY server.go ./
COPY static ./static/

# final configuration
RUN CGO_ENABLED=0 GOOS=linux go build -o /weewoo
EXPOSE 8080
CMD ["/weewoo"]
