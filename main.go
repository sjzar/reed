package main

import (
	"log"

	"github.com/sjzar/reed/cmd/reed"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	reed.Execute()
}
