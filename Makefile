.PHONY: build test test-race lint bench integration clean

build:
	go build ./...

test:
	go test ./...

# -race requires cgo; on Linux/macOS it works out of the box.
# On Windows set CGO_ENABLED=1 and install a C toolchain first.
test-race:
	go test -race ./...

lint:
	go vet ./...

bench:
	go test -bench=. -benchmem ./...

integration:
	go test -tags=integration ./...

clean:
	go clean ./...
