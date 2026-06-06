package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	fmt.Println(string(hash))
	fmt.Println("---")
	// Also verify it works
	err := bcrypt.CompareHashAndPassword(hash, []byte("admin123"))
	if err == nil {
		fmt.Println("VERIFICATION: SUCCESS")
	} else {
		fmt.Println("VERIFICATION FAILED:", err)
	}
}