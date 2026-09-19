
.PHONY: migrate test run fmt vet
DATABASE_PATH ?= data/app.sqlite3

# 可选：用 sqlite3 命令行手动预建库；服务启动时本身也会自动应用内嵌迁移。
migrate:
	mkdir -p $$(dirname "$(DATABASE_PATH)")
	@for f in $$(ls migrations/*.sql | sort); do \
		echo "应用 $$f"; sqlite3 "$(DATABASE_PATH)" < "$$f"; \
	done

test:
	go test ./...

run:
	go run ./cmd/server

fmt:
	gofmt -w cmd internal migrations

vet:
	go vet ./...
