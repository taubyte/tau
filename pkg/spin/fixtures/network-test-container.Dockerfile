# Stage 1: Build the Go binary using golang:bullseye on amd64
# Use the --platform flag to ensure the correct architecture
FROM --platform=linux/amd64 golang:bullseye AS builder

# Set necessary Go environment variables for static linking
ENV CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 GO111MODULE=off

# Set the working directory inside the container
WORKDIR /app

# Embed the Go source code directly into the Dockerfile
COPY <<EOF /app/main.go
package main
import (
    "fmt"
    "net/http"
)

func hello(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintf(w, "Hello, World!")
}

func main() {
    fmt.Println("STARTED")
    defer fmt.Println("STOPPED")
    http.HandleFunc("/", hello)
    http.ListenAndServe(":8080", nil)
}
EOF

# Build the static binary for riscv64
RUN go build -ldflags="-s -w" -o /app/hello .

# Strip unnecessary symbols to reduce binary size
RUN apt-get update && apt-get install -y --no-install-recommends binutils-riscv64-linux-gnu \
    && riscv64-linux-gnu-strip /app/hello \
    && apt-get remove -y binutils-riscv64-linux-gnu \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Stage 2: Create the minimal Docker image using scratch for riscv64
FROM scratch

# Copy the stripped binary from the builder stage
COPY --from=builder /app/hello /hello

# Command to run the binary
ENTRYPOINT ["/hello"]
