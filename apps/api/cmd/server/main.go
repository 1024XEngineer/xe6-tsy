package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(serverAddress(os.Getenv("PORT"))); err != nil {
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
}
