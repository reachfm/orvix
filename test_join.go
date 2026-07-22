package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	home := os.Getenv("HOME")
	fmt.Printf("HOME=%q\n", home)
	result := filepath.Join(home, ".orvix")
	fmt.Printf("Join(HOME, .orvix)=%q\n", result)
}
