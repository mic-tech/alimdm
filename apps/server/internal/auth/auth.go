package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ── Device API keys (opaque, Ali MDM protocol) ─────────────────────────────
// Ali MDM authenticates with `Authorization: Bearer <apiKey>` where apiKey is a
// simple opaque per-device secret issued at enrollment. We generate a 32-byte
// random token and store its SHA-256 hash (so a DB leak doesn't expose live keys).

// GenerateAPIKey returns a new random 48-char hex device key.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashAPIKey is the stored form of a device key.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// VerifyAPIKey constant-time compares a presented key against its stored hash.
func VerifyAPIKey(presented, storedHash string) bool {
	if presented == "" {
		return false
	}
	got := HashAPIKey(presented)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}

// ── Operator session tokens (HS256 JWT) ──────────────────────────────────────

type Signer struct {
	secret []byte
}

func NewSigner(secret string) *Signer { return &Signer{secret: []byte(secret)} }

type Claims struct {
	Sub   string `json:"sub"`
	Kind  string `json:"kind"`  // "operator"
	Email string `json:"email"` // same as Sub; surfaced for the console
	Role  string `json:"role"`  // advisory — the server re-reads the live role
	Iss   string `json:"iss"`
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (s *Signer) Sign(c Claims) (string, error) {
	if c.Iss == "" {
		c.Iss = "ali-mdm"
	}
	if c.Iat == 0 {
		c.Iat = time.Now().Unix()
	}
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	si := b64url(h) + "." + b64url(p)
	sig := hmacSHA256(s.secret, []byte(si))
	return si + "." + b64url(sig), nil
}

func (s *Signer) Verify(tok string) (Claims, error) {
	var c Claims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return c, errors.New("malformed token")
	}
	si := parts[0] + "." + parts[1]
	want := hmacSHA256(s.secret, []byte(si))
	if subtle.ConstantTimeCompare([]byte(b64url(want)), []byte(parts[2])) != 1 {
		return c, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if c.Exp != 0 && time.Now().Unix() > c.Exp {
		return c, errors.New("token expired")
	}
	return c, nil
}

func hmacSHA256(secret, msg []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(msg)
	return mac.Sum(nil)
}

// ── Operator passwords (PBKDF2 via sha256 iteration — no external dep) ──────

// HashPassword derives a verifiable hash. Format: iter$salt$hash (hex).
func HashPassword(password string) (string, error) {
	const iter = 100_000
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := pbkdf2SHA256([]byte(password), salt, iter)
	return fmt.Sprintf("%d|%s|%s", iter, hex.EncodeToString(salt), hex.EncodeToString(h)), nil
}

func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "|")
	if len(parts) != 3 {
		return false
	}
	var iter int
	fmt.Sscanf(parts[0], "%d", &iter)
	salt, _ := hex.DecodeString(parts[1])
	want, _ := hex.DecodeString(parts[2])
	got := pbkdf2SHA256([]byte(password), salt, iter)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 is a minimal PBKDF2-HMAC-SHA256 (1 block) implementation.
func pbkdf2SHA256(password, salt []byte, iter int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	result := make([]byte, len(u))
	copy(result, u)
	for i := 1; i < iter; i++ {
		m2 := hmac.New(sha256.New, password)
		m2.Write(u)
		u = m2.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}
