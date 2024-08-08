.PHONY: dev stop

dev:
	docker compose up -d
	go run cmd/api/main.go

stop:
	docker compose down
	@-pkill -f "go run cmd/api/main.go"