.PHONY: help run

# Application
APP_NAME=kfc-userapi
BINARY_NAME=userapi

help:
	@echo "Available targets:"
	@echo "  run		Run the server"

# Run the application
run:
	@go run cmd/userapi/main.go