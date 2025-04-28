.PHONY: build install clean

build:
	go mod download
	go mod tidy
	go build -o cpe-guesser-go ./cmd/cpe-guesser-go

install: build
	sudo mv cpe-guesser-go /usr/local/bin/

clean:
	rm -f cpe-guesser-go
	go clean -modcache 