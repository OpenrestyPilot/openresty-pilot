package httpapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const sealedPrefix = "enc:v1:"

func orpSeal(value string) (string, error) {
	key, err := hex.DecodeString(os.Getenv("ORP_DATA_KEY"))
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(value), nil)
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func orpOpen(value string) (string, error) {
	if !strings.HasPrefix(value, sealedPrefix) {
		return "", errors.New("stored secret is not encrypted")
	}
	key, err := hex.DecodeString(os.Getenv("ORP_DATA_KEY"))
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid encryption key")
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, sealedPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", errors.New("encrypted value is truncated")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func orpProtect(kind string, body orpDocument) error {
	if kind == "certificates" {
		key, _ := body["privateKey"].(string)
		if key == "" {
			return errors.New("证书私钥不能为空")
		}
		if !strings.HasPrefix(key, sealedPrefix) {
			sealed, err := orpSeal(key)
			if err != nil {
				return err
			}
			body["privateKey"] = sealed
		}
	}
	if kind == "settings" && body["isSensitive"] == true {
		value := strings.TrimSpace(toString(body["value"]))
		if value != "" && !strings.HasPrefix(value, sealedPrefix) {
			sealed, err := orpSeal(value)
			if err != nil {
				return err
			}
			body["value"] = sealed
		}
	}
	if kind == "rbac-users" {
		password, _ := body["password"].(string)
		if password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			body["passwordHash"] = string(hash)
		}
		delete(body, "password")
	}
	return nil
}

func orpRedact(kind string, body orpDocument) {
	if kind == "certificates" {
		delete(body, "privateKey")
	}
	if kind == "settings" && body["isSensitive"] == true {
		body["value"] = "********"
	}
	if kind == "rbac-users" {
		delete(body, "passwordHash")
		delete(body, "password")
	}
}

func toString(value any) string { valueString, _ := value.(string); return valueString }
