// Package crypto provides the cryptographic primitives used by GophKeeper
// to protect user data at rest and to authenticate user passwords.
//
// Two independent mechanisms are provided:
//
//   - Symmetric authenticated encryption of item payloads using AES-256-GCM.
//     A single process-wide key is configured on the server via the
//     GOPHKEEPER_ENCRYPTION_KEY environment variable.
//
//   - Password hashing for user credentials using bcrypt with the default
//     work factor.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/bcrypt"
)

// KeySize is the required length, in bytes, of an AES-256 key.
const KeySize = 32

// ErrInvalidKeySize is returned when a key of the wrong length is supplied.
var ErrInvalidKeySize = errors.New("crypto: key must be exactly 32 bytes")

// ErrDecryptFailed is returned when authentication of the ciphertext fails.
var ErrDecryptFailed = errors.New("crypto: decryption failed")

// Encryptor performs authenticated symmetric encryption using AES-256-GCM.
// A single Encryptor is safe for concurrent use.
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor returns an Encryptor bound to the given 32-byte key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &Encryptor{aead: aead}, nil
}

// GenerateKey returns a fresh random 32-byte key suitable for AES-256.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("crypto: generate key: %w", err)
	}
	return key, nil
}

// Encrypt seals plaintext and returns the ciphertext together with the
// nonce required to decrypt it. A fresh random nonce is generated on every
// call; callers must persist both values.
func (e *Encryptor) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: generate nonce: %w", err)
	}
	ciphertext = e.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt opens ciphertext using the given nonce and returns the original
// plaintext. If the ciphertext has been tampered with, or the nonce is of
// the wrong length, ErrDecryptFailed is returned.
func (e *Encryptor) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) != e.aead.NonceSize() {
		return nil, ErrDecryptFailed
	}
	plaintext, err := e.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// HashPassword returns a bcrypt hash of the given password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("crypto: hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether password matches the given bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NonceSize returns the length in bytes of the nonce that Decrypt
// requires. The value is fixed by the AES-GCM construction and is
// currently 12.
func (e *Encryptor) NonceSize() int { return e.aead.NonceSize() }
