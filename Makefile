build:
	go build

test:
	go test

deps:
	rm -rf Godeps
	godep save
