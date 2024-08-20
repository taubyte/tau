# Stage 1: Build the executables
FROM riscv64/ubuntu:latest AS builder

# Update and install required packages
RUN apt-get update && apt-get install -y \
    git \
    build-essential \
    zlib1g-dev

# Clone the squashfs-tools repository
RUN git clone https://github.com/plougher/squashfs-tools.git /squashfs-tools

# Set the working directory
WORKDIR /squashfs-tools/squashfs-tools

RUN mkdir -p /out/bin

ENV CFLAGS="-static"
ENV LDFLAGS="-static" 

# Build the project and install it to the custom directory
RUN make 
RUN make install INSTALL_PREFIX=/out

RUN ls /out/bin/mksquashfs

# Strip all binaries in the install directory
RUN find /out/bin -type f -exec strip {} \;

# Stage 2: Create the final image with only necessary executables
FROM riscv64/alpine:latest

# Copy the stripped executables from the builder stage
COPY --from=builder /out/bin/ /bin/
