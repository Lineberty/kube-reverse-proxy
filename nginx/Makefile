TAG ?= 0.1.0
PREFIX ?= quay.io/lineberty

all: container

container:
	docker build -t $(PREFIX)/nginx-confd:$(TAG) .

push:
	docker push $(PREFIX)/nginx-confd:$(TAG)

clean:
