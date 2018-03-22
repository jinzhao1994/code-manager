all: build

fmt:
	goimports -w .

build: fmt
	go build .

install: fmt
	go install .
