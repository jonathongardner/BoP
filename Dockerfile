# Stage 1: Build the Go application
FROM golang:1.24 AS builder

# Set the working directory inside the container
WORKDIR /bop

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the Go source code
COPY . .

# Build the Go application
# CGO_ENABLED=0 disables CGO, making the binary statically linked
# GOOS=linux ensures the binary is built for Linux
# -o app specifies the output binary name
RUN CGO_ENABLED=0 GOOS=linux go build -o bop .

# Stage 2: Create a minimal image to run the application
FROM scratch

# Copy the built binary from the builder stage
COPY --from=builder /bop/bop .

# Expose the port your application listens on (optional, but good practice)
EXPOSE 80

WORKDIR ["/data/"]

# Define the command to run when the container starts
ENTRYPOINT ["/bop"]
