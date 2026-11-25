//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("ClassNote Studio is a Windows desktop app. Build with GOOS=windows GOARCH=amd64 go build.")
}
