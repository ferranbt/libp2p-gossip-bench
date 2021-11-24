
.PHONY: protoc
protoc:
	protoc --go_out=. --go-grpc_out=. ./proto/*.proto
