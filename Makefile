SHELL=/bin/bash

ldflags := -X 'github.com/livepeer/stream-tester/model.Version=$(shell git describe --dirty)'
# ldflags := -X 'github.com/livepeer/stream-tester/model.Version=$(shell git describe --dirty)' -X 'github.com/livepeer/stream-tester/model.IProduction=true'

all: transcode

.PHONY: transcode
transcode:
	go build -ldflags="$(ldflags)" -o build/livepeer-transcode cmd/transcode/*.go
