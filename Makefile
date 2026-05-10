.PHONY: build test lint fmt vet vuln clean docker-build docker-up docker-down docker-logs

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

docker-build:
	docker build -t networkinventoryagent .

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f
