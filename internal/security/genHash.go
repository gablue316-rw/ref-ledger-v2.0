package main

import (
	"fmt"

	"flag"

	"golang.org/x/crypto/bcrypt"
)

func main() {

	var password = flag.String("p", "", "Enter password to hash")
	flag.Parse()
	pwd := *password

	fmt.Println("Hashing password", pwd)
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(hash))
}
