package auth

import (
	"os"
	"testing"
)

const testUsersConf = `# test credentials
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
user1:04f8996da763b7a969b1028ee3007569eaf3a635486ddab211d512c85b9df8fb
`

func setupTest(t *testing.T, confContent string) string {
	t.Helper()
	f, err := os.CreateTemp("", "users.conf.*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(confContent); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestCheckPassword_ValidLogin(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if !CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword(admin, admin) should return true")
	}
	if !CheckPassword(path, "user1", "user") {
		t.Error("CheckPassword(user1, user) should return true")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if CheckPassword(path, "admin", "wrongpassword") {
		t.Error("CheckPassword(admin, wrongpassword) should return false")
	}
}

func TestCheckPassword_NonexistentUser(t *testing.T) {
	path := setupTest(t, testUsersConf)
	defer os.Remove(path)

	if CheckPassword(path, "nonexistent", "anything") {
		t.Error("CheckPassword(nonexistent, anything) should return false")
	}
}

func TestCheckPassword_EmptyFile(t *testing.T) {
	path := setupTest(t, "")
	defer os.Remove(path)

	if CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword on empty file should return false")
	}
}

func TestCheckPassword_Comments(t *testing.T) {
	conf := `# this is a comment

# another comment
admin:8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
`
	path := setupTest(t, conf)
	defer os.Remove(path)

	if !CheckPassword(path, "admin", "admin") {
		t.Error("CheckPassword should ignore comments and blanks")
	}
}
