# Use an official Go runtime as a parent image
FROM golang:1.21-bullseye

# Set the working directory in the container
WORKDIR /app

# Install poppler-utils and bibtool
RUN apt-get update && apt-get install -y poppler-utils bibtool

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go app for production
RUN go build -o main ./cmd/api

# Command to run the executable
CMD ["./main"]