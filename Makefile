.PHONY: test run serve replay fmt vet runtime

BIN := crucible

fmt:
	gofmt -w $(shell find . -name '*.go' -not -path './.git/*')

vet:
	go vet ./...

runtime:
	python3 -m pip install -r runtime/requirements.txt

test:
	go test ./...
	PYTHONPATH=runtime python3 -m crucible_rt smoke

build:
	go build -o $(BIN) ./cmd/crucible

serve: build
	./$(BIN) serve -addr 127.0.0.1:8080

run: build
	./$(BIN) run -seed 42 -trials 40 -p 0.3

replay: build
	./$(BIN) replay -seed 42 -trial 0 -p 0.3
