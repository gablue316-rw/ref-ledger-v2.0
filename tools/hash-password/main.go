package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Printf(
			"Usage: %s <username> <password>\n",
			os.Args[0],
		)
		os.Exit(1)
	}

	username := os.Args[1]
	password := os.Args[2]

	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		log.Fatalf("Unable to hash password: %v", err)
	}

	fmt.Printf("Username: %s\n", username)
	fmt.Printf("Password hash: %s\n", passwordHash)
}
