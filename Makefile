BINARY := elastic-pvc
IMAGE  ?= elastic-pvc
TAG    ?= latest

.PHONY: build test clean docker-build lint fmt vet

build:
	go build -o bin/$(BINARY) ./cmd/

test:
	go test ./... -v -count=1

clean:
	rm -rf bin/

docker-build:
	docker build -t $(IMAGE):$(TAG) .

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet
