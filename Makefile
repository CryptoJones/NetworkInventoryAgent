.PHONY: build test lint fmt vet vuln clean

build:
	go build ./...

test:
	go test -race ./...

lint: fmt vet

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted files:\n$$out"; exit 1; fi

vet:
	go vet ./...

vuln:
	govulncheck ./...

clean:
	rm -f wintermute neuromancer agent
