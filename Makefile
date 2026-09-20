.PHONY: build test smoke
build:
	mkdir -p bin
	CGO_ENABLED=1 go build -trimpath -o bin/rift-ledger-go ./cmd/server
test:
	go vet ./...
	go test -race -count=1 ./...
smoke:
	python3 scripts/smoke.py
