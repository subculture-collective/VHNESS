.PHONY: verify verify-core verify-curation test test-integration
verify-curation:
	cd curation && uv sync --frozen && uv run --frozen pytest -q && uv run --frozen ruff check . && uv run --frozen ruff format --check .
	node --check curation/arkhiv_curation/static/app.js
verify-core:
	cd backend && test -z "$$(gofmt -l cmd internal)" && go vet ./... && go test -race ./... && go build ./cmd/vhness
verify: verify-core
	pnpm install --frozen-lockfile
	pnpm build:packages
	pnpm check:packages
	python3 scripts/check_package_consumer.py
	python3 scripts/check_core_pin.py
	python3 -m unittest discover -s scripts -p 'test_*.py'
	pnpm --dir web typecheck
	pnpm --dir web lint
	pnpm --dir web test
	pnpm --dir web build
test:
	pnpm build:packages
	pnpm --filter "./packages/*" -r test
	cd backend && go test -race ./...
	pnpm --dir web test
test-integration:
	test -n "$(TEST_DATABASE_URL)"
	cd backend && go test -race ./internal/catalog ./internal/worker ./internal/sourcecatalog -count=1
