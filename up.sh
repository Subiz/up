#!/bin/sh -e
UPVERSION=4.0.1

[ "$1" = "update" ] && NEWV=$(curl -L https://github.com/subiz/up/releases/download/0/stable.txt) && curl -L https://github.com/subiz/up/releases/download/$NEWV/up.sh -o $GOPATH/bin/up4 && chmod +x $GOPATH/bin/up4 && echo $NEWV && exit 0

[ "$1" = "help" ] && printf "subiz up v$UPVERSION\ncommands: help, update\n" && exit 0

[ ! -f ./build.yaml ] && echo "missing build.yaml" && exit 0
export _VERSION=$(date +%s)
printf "\e[36mVERSION $_VERSION\e[m\n"

printf "\e[93mBUILDING... \e[m\n"
starttime=$(date +%s.%N)
echo "#!/bin/sh" > .build.tmp
dockerun build.yaml >> .build.tmp
chmod +x .build.tmp && ./.build.tmp
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

printf "\e[93mDOCKERING... \e[m\n"
starttime=$(date +%s.%N)
export DOCKER_HOST=tcp://dev.subiz.net:2376
cp Dockerfile .Dockerfile.tmp
configmap -config=../devconfig/config.yaml -format=docker -compact configmap.yaml >> .Dockerfile.tmp
docker build -q -t $_DOCKERHOST$_ORG/$_NAME:$_VERSION -f .Dockerfile.tmp .
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

printf "\e[93mDEPLOYING... \e[m\n"
starttime=$(date +%s.%N)
export IMG="$_DOCKERHOST$_ORG/$_NAME:$_VERSION"
envsubst < deploy.$_ENV.yaml > .deploy.$_ENV.yaml
kubectl apply -f .deploy.$_ENV.yaml
rm .deploy.$_ENV.yaml
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

printf "\e[93mCLEANING... \e[m\n"
starttime=$(date +%s.%N)
rm .Dockerfile.tmp
rm .build.tmp
rm -f $_NAME.tar.gz
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)
