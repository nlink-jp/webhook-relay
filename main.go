package main

import (
	"log"
	"os"

	"github.com/nlink-jp/webhook-relay/cmd"
)

var version = "dev"

func main() {
	log.SetFlags(0) // Cloud Run adds timestamps; avoid duplicate
	if err := cmd.Run(version); err != nil {
		log.Printf("FATAL: %v", err)
		os.Exit(1)
	}
}
