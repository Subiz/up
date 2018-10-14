#!/bin/sh
echo "subiz up v4.0.1. "
echo "Update your script: `curl -L https://github.com/subiz/up/releases/download/$(curl -s https://github.com/subiz/up/releases/download/0/stable.txt)/up.sh -O $GOPATH/bin/up4`"

[ ! -f ./build.yaml ] && echo "missing build.yaml" && exit 0

export _VERSION=$(date +%s)
echo "VERSION $_VERSION"
export _NAME=payment
export _ENV=dev
export _ORG=subiz
export _DOCKERHOST=

echo "$(date) BUILDING... "
echo "#!/bin/sh" > .build.tmp
dockerun build.yaml >> .build.tmp
chmod +x .build.tmp && ./.build.tmp

echo "$(date) DOCKERING..."
export DOCKER_HOST=tcp://dev.subiz.net:2376
cp Dockerfile .Dockerfile.tmp
configmap -config=../devconfig/config.yaml -format=docker -compact configmap.yaml >> .Dockerfile.tmp
docker build -q -t $_DOCKERHOST$_ORG/$_NAME:$_VERSION -f .Dockerfile.tmp .

echo "$(date) DEPLOYING..."
export IMG="$_DOCKERHOST$_ORG/$_NAME:$_VERSION"
envsubst < deploy.$_ENV.yaml > .deploy.$_ENV.yaml
kubectl apply -f .deploy.$_ENV.yaml
rm .deploy.$_ENV.yaml

echo "$(date) CLEANING..."
rm .Dockerfile.tmp
rm .build.tmp
rm -f $_NAME.tar.gz
echo "$(date) DONE."
