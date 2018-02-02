#!/bin/sh -e

go build -i
version=`./up -V | cut -d ' ' -f 3`
docker build -t subiz/up:$version .
docker push subiz/up:$version
