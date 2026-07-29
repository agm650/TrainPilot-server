package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type PasswordParams struct {
	Iterations int
	SaltLength int
	KeyLength  int
}

func DefaultPasswordParams() PasswordParams {
	return PasswordParams{Iterations: 600_000, SaltLength: 16, KeyLength: 32}
}

func HashPassword(password string, p PasswordParams) (string, error) {
	if len(password) < 10 {
		return "", errors.New("password must contain at least 10 characters")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256([]byte(password), salt, p.Iterations, p.KeyLength)
	return fmt.Sprintf("$pbkdf2-sha256$i=%d$%s$%s", p.Iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[1] != "pbkdf2-sha256" {
		return false, errors.New("invalid password hash")
	}
	kv := strings.SplitN(parts[2], "=", 2)
	if len(kv) != 2 || kv[0] != "i" {
		return false, errors.New("invalid password parameters")
	}
	iterations, err := strconv.Atoi(kv[1])
	if err != nil || iterations < 100_000 {
		return false, errors.New("invalid password iteration count")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, err
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	actual := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	hLen := sha256.Size
	blocks := (keyLength + hLen - 1) / hLen
	output := make([]byte, 0, blocks*hLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		output = append(output, t...)
	}
	return output[:keyLength]
}
