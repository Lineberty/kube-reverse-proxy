TAG = 0.1.0
PREFIX = quay.io/lineberty

all: container

container:
	sudo docker build -t $(PREFIX)/nginx-confd:$(TAG) .

push:
	sudo docker push $(PREFIX)/nginx-confd:$(TAG)

clean:
