package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestBuildJWTUsesPS256AndExpectedClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	path := filepath.Join(t.TempDir(), "private.json")
	raw, _ := json.Marshal(map[string]any{"key_id": "kid", "sub_account": "sub", "private_key": privatePEM})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	account, err := LoadServiceAccount(path)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().Unix()
	tokenText, err := BuildJWT(account, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Parse(tokenText, func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithAudience(TokenAudience), jwt.WithValidMethods([]string{"PS256"}))
	if err != nil || !token.Valid {
		t.Fatalf("parse token: %v", err)
	}
	if token.Header["kid"] != "kid" || token.Header["typ"] != "JWT" || token.Header["alg"] != "PS256" {
		t.Fatalf("header = %#v", token.Header)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["iss"] != "sub" || int64(claims["iat"].(float64)) != issuedAt || int64(claims["exp"].(float64)) != issuedAt+TokenLifetimeSecond {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestRejectsPrivateJSONWithoutKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte(`{"key_id":"kid","sub_account":"sub"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServiceAccount(path); err == nil {
		t.Fatal("expected an error")
	}
}
