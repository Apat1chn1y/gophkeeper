package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateKeySize(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("key size = %d, want %d", len(key), KeySize)
	}
}

func TestNewEncryptorRejectsWrongKeySize(t *testing.T) {
	if _, err := NewEncryptor(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	plaintext := []byte("секретные данные")
	ct, nonce, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := enc.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypted = %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	_, n1, _ := enc.Encrypt([]byte("x"))
	_, n2, _ := enc.Encrypt([]byte("x"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonce reused across calls")
	}
}

func TestDecryptDetectsTampering(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	ct, nonce, _ := enc.Encrypt([]byte("payload"))
	ct[0] ^= 0xff
	if _, err := enc.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected authentication failure")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	enc1, _ := NewEncryptor(key1)
	enc2, _ := NewEncryptor(key2)
	ct, nonce, _ := enc1.Encrypt([]byte("payload"))
	if _, err := enc2.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected failure with wrong key")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "hunter2") {
		t.Fatal("VerifyPassword failed for correct password")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("VerifyPassword succeeded for wrong password")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("hashes should differ due to random salt")
	}
}

func TestEncryptDecryptEmpty(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	ct, nonce, err := enc.Encrypt(nil)
	if err != nil {
		t.Fatalf("Encrypt(nil): %v", err)
	}
	got, err := enc.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestNonceSize(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	if enc.NonceSize() != 12 {
		t.Fatalf("NonceSize() = %d, want 12", enc.NonceSize())
	}
}
