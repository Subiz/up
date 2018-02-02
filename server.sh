#!/bin/sh -e

port=${1:-8080}
echo -en "HTTP/1.1 200 OK\r\nConnection: keep-alive\r\n\r\n`cat up.b64`\r\n" > resp
echo "Starting web server on port ${port}"
while true ; do nc -q 1 -l ${port} < resp ; done
