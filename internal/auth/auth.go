// Package auth loads a Service Account key and signs the short-lived PS256 JWT.
package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenAudience       = "https://oauth-login.cloud.huawei.com/oauth2/v3/token"
	TokenLifetimeSecond = 3600
)

// ServiceAccount contains only the fields needed for request signing.
type ServiceAccount struct {
	KeyID      string
	SubAccount string
	key        *rsa.PrivateKey
}

// LoadServiceAccount validates private.json and parses its PKCS8 RSA key.
func LoadServiceAccount(path string) (*ServiceAccount, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("private.json is not valid JSON")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return nil, errors.New("private.json is not valid JSON")
	}
	keyID, ok1 := payload["key_id"].(string)
	subAccount, ok2 := payload["sub_account"].(string)
	privatePEM, ok3 := payload["private_key"].(string)
	if !ok1 || !ok2 || !ok3 || strings.TrimSpace(keyID) == "" || strings.TrimSpace(subAccount) == "" || strings.TrimSpace(privatePEM) == "" {
		return nil, errors.New("private.json must contain key_id, sub_account, and private_key")
	}
	if !strings.Contains(privatePEM, "BEGIN PRIVATE KEY") {
		return nil, errors.New("private_key must be a PKCS8 PEM private key")
	}
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return nil, errors.New("private_key cannot be loaded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private_key cannot be loaded")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private_key cannot be loaded")
	}
	return &ServiceAccount{KeyID: keyID, SubAccount: subAccount, key: key}, nil
}

// BuildJWT returns a PS256 JWT whose private key never leaves memory.
func BuildJWT(account *ServiceAccount, issuedAt int64) (string, error) {
	claims := jwt.MapClaims{
		"aud": TokenAudience,
		"iss": account.SubAccount,
		"iat": issuedAt,
		"exp": issuedAt + TokenLifetimeSecond,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodPS256, claims)
	token.Header["kid"] = account.KeyID
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(account.key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

// IssuedAtSeconds is a small helper used by command and doctor code.
func IssuedAtSeconds(now time.Time) int64 { return now.Unix() }
