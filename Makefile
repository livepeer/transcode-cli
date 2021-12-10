SHELL=/bin/bash

all: cli-transcoder

ldflags := -X 'github.com/livepeer/stream-tester/model.Version=$(shell git describe --dirty)'
# ldflags := -X 'github.com/livepeer/stream-tester/model.Version=$(shell git describe --dirty)' -X 'github.com/livepeer/stream-tester/model.IProduction=true'


.PHONY: cli-transcoder
cli-transcoder:
	go build -ldflags="$(ldflags)" cmd/cli-transcoder/cli-transcoder.go
