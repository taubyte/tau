# Stage 1: Build the executables
FROM riscv64/ubuntu:24.10 AS builder

# Update and install required packages
RUN apt-get update && apt-get install -y \
    git \
    build-essential \
    zlib1g-dev pkg-config uuid-dev \
    autoconf automake libtool liblz4-dev

RUN mkdir -p /out/bin

# Clone the squashfs-tools repository
RUN git clone https://github.com/plougher/squashfs-tools.git /squashfs-tools

WORKDIR /squashfs-tools/squashfs-tools

ENV CFLAGS="-static"
ENV LDFLAGS="-static" 

RUN make 
RUN make install INSTALL_PREFIX=/out

# Clone the erofs
RUN git clone https://github.com/erofs/erofs-utils.git /erofs-utils

# Set the working directory
WORKDIR /erofs-utils

RUN mkdir -p m4
RUN ./autogen.sh
RUN ./configure --prefix=/out
RUN make
RUN make install 

# Strip all binaries in the install directory
RUN find /out/bin -type f -exec strip {} \;

# Stage 2: Create the final image with only necessary executables
FROM riscv64/ubuntu:24.10

RUN apt-get update && apt-get install -y genisoimage

#RUN apk update && apk add --no-progress cdrkit

RUN apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Copy the stripped executables from the builder stage
COPY --from=builder /out/bin/ /bin/
