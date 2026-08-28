.PHONY: all test build gate fmt vet clean

all: fmt vet test

test:
	go test ./... -count=1

build:
	go build ./...

gate:
	go run ./cmd/gate

fmt:
	gofmt -l -w .

vet:
	go vet ./...

clean:
	rm -rf bin/
