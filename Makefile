.PHONY: build test lint golangci fmt vet vuln clean docker-build docker-up docker-down docker-logs

build:
	go build ./...

test:
	go test -race ./...

# `make lint` is the local CI equivalent: format, vet, then full static-analysis
# pass via golangci-lint (configured in .golangci.yml).
lint: fmt vet golangci

golangci:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed — install from https://golangci-lint.run/"; exit 1; }
	golangci-lint run ./...

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted files:\n$$out"; exit 1; fi

vet:
	go vet ./...

vuln:
	govulncheck ./...

clean:
	rm -f wintermute neuromancer agent

docker-build:
	docker build -t networkinventoryagent .

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f
