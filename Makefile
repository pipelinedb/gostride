build:
	go build

test:
	go test -v

deps:
	rm -rf Godeps
	godep save
