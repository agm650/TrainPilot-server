package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	params := PasswordParams{Iterations: 100_000, SaltLength: 16, KeyLength: 32}
	hash, err := HashPassword("a-secure-password", params)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "a-secure-password")
	if err != nil || !ok {
		t.Fatalf("expected valid password, ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("wrong password unexpectedly accepted")
	}
}

func TestPasswordMinimumLength(t *testing.T) {
	_, err := HashPassword("short", DefaultPasswordParams())
	if err == nil {
		t.Fatal("expected short password to be rejected")
	}
}
