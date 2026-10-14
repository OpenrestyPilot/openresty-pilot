package httpapi

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestOrpSealAndOpen(t *testing.T) {
	t.Setenv("ORP_DATA_KEY", strings.Repeat("a", 64))
	secret := "-----BEGIN PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----"
	sealed, err := orpSeal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("private key was stored in plaintext")
	}
	plain, err := orpOpen(sealed)
	if err != nil || plain != secret {
		t.Fatalf("round trip failed: %v", err)
	}
	t.Setenv("ORP_DATA_KEY", strings.Repeat("b", 64))
	if _, err := orpOpen(sealed); err == nil {
		t.Fatal("wrong key decrypted a secret")
	}
}

func TestVerifyTOTP(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	if !verifyTOTP(secret, "287082", time.Unix(59, 0)) {
		t.Fatal("RFC 6238 SHA1 test vector failed")
	}
	if verifyTOTP(secret, "287083", time.Unix(59, 0)) {
		t.Fatal("incorrect OTP accepted")
	}
}
