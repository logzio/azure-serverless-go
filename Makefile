
.PHONY: function-local
function-local:
	go build logs-function/handler.go

.PHONY: function-cloud-linux
function-cloud-linux:
	GOOS=linux   GOARCH=amd64 go build logs-function/handler.go