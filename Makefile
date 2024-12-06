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

# Generate the protobuf files
proto-gen:
	@echo "Generating protobuf files..."
	@find proto -name '*.proto' -print0 | xargs -0 -n1 \
		protoc \
			--go_out=. \
			--go_opt=module=github.com/ramisoul84/kfc-userapi \
			--go-grpc_out=. \
			--go-grpc_opt=module=github.com/ramisoul84/kfc-userapi \
			--proto_path=.
	@echo "✅ Proto files generated"