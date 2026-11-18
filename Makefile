GO      ?= go
export GOTOOLCHAIN ?= go1.27.2

.PHONY: build web test run dev clean

build: web            ## build frontend + single binary bin/lifeagent
	$(GO) build -o bin/lifeagent ./cmd/lifeagent

web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	touch web/dist/.gitkeep   # vite empties dist/; keep the embed dir in git

test:
	$(GO) vet ./... && $(GO) test ./...

run: build            ## run with ./config.yaml
	./bin/lifeagent -config config.yaml

dev:                  ## backend only; run `cd web && pnpm dev` in another shell
	$(GO) run ./cmd/lifeagent -config config.yaml -debug

clean:
	rm -rf bin web/dist/assets web/dist/index.html
