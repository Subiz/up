#!/bin/sh -e
UPVERSION=4.0.2

[ "$1" = "update" ] && NEWV=$(curl -L https://github.com/subiz/up/releases/download/0/stable.txt) && curl -L https://github.com/subiz/up/releases/download/$NEWV/up.sh -o $GOPATH/bin/up4 && chmod +x $GOPATH/bin/up4 && echo $NEWV && exit 0

[ "$1" = "help" ] && printf "subiz up v$UPVERSION\ncommands: help, update\n" && exit 0

[ ! -f ./build.yaml ] && echo "missing build.yaml" && exit 0
export _VERSION=$(date +%s)
printf "\e[36mVERSION $_VERSION\e[m\n"

# ===========================================
printf "\e[93mBUILDING... \e[m\n"
starttime=$(date +%s.%N)
echo "#!/bin/sh" > /tmp/$_NAME.build
dockerun build.yaml >> /tmp/$_NAME.build
chmod +x /tmp/$_NAME.build && /tmp/$_NAME.build
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

# ===========================================
printf "\e[93mDOCKERING... \e[m\n"
starttime=$(date +%s.%N)
cp Dockerfile /tmp/$_NAME.Dockerfile
configmap -config=../devconfig/config.yaml -format=docker -compact configmap.yaml >> /tmp/$_NAME.Dockerfile
DOCKER_HOST=$DOCKER_BUILD_HOST docker build -t $_DOCKERHOST$_ORG/$_NAME:$_VERSION -f /tmp/$_NAME.Dockerfile .
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

# ===========================================
printf "\e[93mDEPLOYING... \e[m\n"
starttime=$(date +%s.%N)
export IMG="$_DOCKERHOST$_ORG/$_NAME:$_VERSION"
export GUID=$(date +%s)
envsubst < deploy.$_ENV.yaml > .deploy.$_ENV.yaml

[ -z $KUBECTL ] && KUBECTL=kubectl
$KUBECTL apply -f .deploy.$_ENV.yaml
rm .deploy.$_ENV.yaml
printf "\e[32m(%.1f sec)\e[m\n" $(echo "$(date +%s.%N) - $starttime" | bc)

rm -f $_NAME.tar.gz
