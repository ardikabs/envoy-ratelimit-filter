# Local Envoy rate-limit filter (gonvoy, Envoy v1.40).
#
# test runs the full suite: unit (race) + e2e against real Envoy.
# filter.so artifacts are linux builds via Docker: a macOS-built .so is
# Mach-O and Envoy rejects it with "invalid ELF header".

GO := env -u GOROOT go
GOBUILDER := golang:1.26.10-bookworm
ENVOY_IMAGE := envoyproxy/envoy:contrib-v1.40-latest

FILTER_SO := filter.so
E2E_FILTER_DIR := test/e2e/filters/ratelimit
E2E_FILTER_SO := $(E2E_FILTER_DIR)/filter.so

.PHONY: help build vet lint test test-unit test-e2e playground playground-demo playground-down clean

help:
	@echo "Targets:"
	@echo "  build            linux filter.so at ./filter.so (via Docker)"
	@echo "  vet              go vet (default + e2e tags)"
	@echo "  lint             gofmt clean check"
	@echo "  test             unit (race) + e2e vs real Envoy"
	@echo "  test-unit        unit tests only"
	@echo "  test-e2e         e2e vs real Envoy only (builds fixture .so via Docker)"
	@echo "  playground       build + start Envoy on :10000 (detached)"
	@echo "  playground-demo  run the curl allow/deny demo"
	@echo "  playground-down  stop the playground Envoy"
	@echo "  clean            remove built filter.so files"

build:
	docker run --rm -v $(CURDIR):/app -w /app $(GOBUILDER) \
		bash -c "GOFLAGS=-buildvcs=false go build -o /app/$(FILTER_SO) -buildmode=c-shared ."

vet:
	$(GO) vet ./...
	$(GO) vet -tags e2e ./test/e2e/

lint:
	test -z "$$(gofmt -l .)"

test: test-unit test-e2e

test-unit:
	$(GO) test ./... -race -count=1

test-e2e:
	docker run --rm -v $(CURDIR):/app -w /app/$(E2E_FILTER_DIR) $(GOBUILDER) \
		bash -c "GOFLAGS=-buildvcs=false go build -o filter.so -buildmode=c-shared ."
	$(GO) test -tags e2e ./test/e2e/ -run TestRatelimit -count=1 -v; status=$$?; rm -f $(E2E_FILTER_SO); exit $$status

playground:
	./playground/run.sh

playground-demo:
	./playground/curl.sh

playground-down:
	docker stop envoy-ratelimit-playground || true

clean:
	rm -f $(FILTER_SO) playground/filter.so $(E2E_FILTER_SO)
