# Makefile for the Docker image gcr.io/google_containers/kube2sky
# MAINTAINER: Tim Hockin <thockin@google.com>
# If you update this image please bump the tag value before pushing.

.PHONY: all kube2nginx container push clean test

TAG ?= 0.1.0
PREFIX ?= quay.io/lineberty

all: container

kube2nginx: kube2nginx.go
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 godep go build -a -installsuffix cgo --ldflags '-w' ./kube2nginx.go

container: kube2nginx
	docker build -t $(PREFIX)/kube2nginx:$(TAG) .

push:
	docker push $(PREFIX)/kube2nginx:$(TAG)

clean:
	rm -f kube2nginx

test: clean
	godep go test -v --vmodule=*=4
