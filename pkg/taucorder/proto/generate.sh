#!/bin/bash

go install github.com/bufbuild/buf/cmd/buf@latest && \
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest && \
npm i -g @connectrpc/protoc-gen-es && \ 
npm i -g @bufbuild/protoc-gen-es && \
buf generate 