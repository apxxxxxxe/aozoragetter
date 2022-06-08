build: main.go go.mod go.sum
	go build
	GOOS=windows GOARCH=386 go build
