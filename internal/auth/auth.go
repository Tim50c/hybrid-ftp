package auth

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"strings"
)

func CheckPassword(usersFile, username, password string) bool {
	f, err := os.Open(usersFile)
	if err != nil {
		log.Printf("[AUTH] cannot open users file %s: %v", usersFile, err)
		return false
	}
	defer f.Close()

	hash := sha256.Sum256([]byte(password))
	expectedHash := fmt.Sprintf("%x", hash)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == username {
			match := strings.EqualFold(parts[1], expectedHash)
			if match {
				log.Printf("[AUTH] user %s authenticated", username)
			} else {
				log.Printf("[AUTH] user %s password mismatch", username)
			}
			return match
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[AUTH] error reading users file: %v", err)
	}
	log.Printf("[AUTH] user %s not found", username)
	return false
}
