#!/bin/sh -e

CGO_ENABLED=0 go build -ldflags="-s -w"
version=`./up -V | cut -d ' ' -f 3`
docker build -t subiz/up:$version .
docker push subiz/up:$version
