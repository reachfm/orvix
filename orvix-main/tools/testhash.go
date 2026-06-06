package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	// This is the exact hash from the database
	storedHash := "$2a$10$hNqe4./8Okt4oZ8mzVcHJOjYmz4QPB6CbGYQEBfh2A1/LfoiYdYr."
	password := "admin123"

	fmt.Println("Testing bcrypt verification...")
	fmt.Println("Stored hash:", storedHash)
	fmt.Println("Password:", password)

	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
	if err == nil {
		fmt.Println("SUCCESS: Password matches!")
	} else {
		fmt.Println("FAILED:", err)
	}

	// Also test generating a new hash
	newHash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	fmt.Println("\nNew hash:", string(newHash))

	// Verify the new hash works
	err = bcrypt.CompareHashAndPassword(newHash, []byte(password))
	if err == nil {
		fmt.Println("New hash verification: SUCCESS")
	}
}