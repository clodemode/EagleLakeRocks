package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "elr_session"
const sessionTTL = 30 * 24 * time.Hour

type Auth struct {
	passwordHash []byte // bcrypt, from ADMIN_PASSWORD_HASH
	signingKey   []byte // from SESSION_KEY, or random per-boot
	enabled      bool
}

func NewAuth() *Auth {
	a := &Auth{}
	if h := os.Getenv("ADMIN_PASSWORD_HASH"); h != "" {
		a.passwordHash = []byte(h)
		a.enabled = true
	}
	if k := os.Getenv("SESSION_KEY"); k != "" {
		a.signingKey = []byte(k)
	} else {
		// No key set: generate one. Sessions then die on restart, which is the
		// safe failure mode — it logs everyone out rather than signing with a
		// predictable key.
		a.signingKey = make([]byte, 32)
		_, _ = rand.Read(a.signingKey)
	}
	return a
}

func (a *Auth) Check(password string) bool {
	if !a.enabled {
		return false
	}
	return bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.signingKey)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func (a *Auth) Issue(w http.ResponseWriter, secure bool) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := strconv.FormatInt(exp, 10)
	val := base64.RawURLEncoding.EncodeToString([]byte(payload + "." + a.sign(payload)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: val, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, Expires: time.Unix(exp, 0),
	})
}

func (a *Auth) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

func (a *Auth) IsAuthed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(raw), ".", 2)
	if len(parts) != 2 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(a.sign(parts[0])), []byte(parts[1])) != 1 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	return err == nil && time.Now().Unix() < exp
}

// HashPassword is the helper behind `eagle-lake-rocks -hash <password>`, so an
// operator can mint ADMIN_PASSWORD_HASH without a separate tool.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return string(h), nil
}
