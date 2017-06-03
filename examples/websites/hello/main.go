package main

/*
 * Enter directory examples/websites/hello
 * go run ./main.go
 */

import "github.com/justintan/wine"

func main() {
	s := wine.Default()
	s.StaticDir("/", "./html")
	s.Run(":8000")
}