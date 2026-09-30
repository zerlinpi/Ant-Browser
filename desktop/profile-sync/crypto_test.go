package profilesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedArchiveRoundTripAcrossChunks(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "profile.zip")
	content := bytes.Repeat([]byte("profile-data-"), (int(encryptionChunkSize)/len("profile-data-"))+100)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	copy(key[:], testEncryptionKey)
	limits := Limits{MaxArchiveBytes: int64(len(content)) + 1<<20}
	encrypted, summary, err := encryptArchive(context.Background(), source, key, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encrypted)
	ciphertext, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(ciphertext)
	if summary.Size != int64(len(ciphertext)) || summary.Hash != hex.EncodeToString(digest[:]) {
		t.Fatalf("invalid ciphertext summary: %+v bytes=%d", summary, len(ciphertext))
	}
	if bytes.Contains(ciphertext, []byte("profile-data-")) {
		t.Fatal("ciphertext exposes plaintext")
	}
	decrypted, err := decryptArchive(context.Background(), encrypted, key, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(decrypted)
	got, err := os.ReadFile(decrypted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("decrypted bytes differ: got=%d want=%d", len(got), len(content))
	}
}

func TestEncryptedArchiveRejectsWrongKeyTamperingAndTruncation(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "profile.zip")
	if err := os.WriteFile(source, bytes.Repeat([]byte("sensitive"), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	copy(key[:], testEncryptionKey)
	limits := Limits{MaxArchiveBytes: 1 << 20}
	encrypted, _, err := encryptArchive(context.Background(), source, key, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encrypted)
	var wrongKey [32]byte
	copy(wrongKey[:], bytes.Repeat([]byte{0x24}, 32))
	if _, err := decryptArchive(context.Background(), encrypted, wrongKey, limits); !errors.Is(err, ErrInvalidProfileCiphertext) {
		t.Fatalf("wrong-key error = %v", err)
	}
	original, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutated := range map[string][]byte{
		"tampered":  append([]byte(nil), original...),
		"truncated": append([]byte(nil), original[:len(original)-1]...),
		"trailing":  append(append([]byte(nil), original...), 0),
	} {
		if name == "tampered" {
			mutated[encryptedHeaderSize+5] ^= 0x80
		}
		path := filepath.Join(t.TempDir(), name+".enc")
		if err := os.WriteFile(path, mutated, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := decryptArchive(context.Background(), path, key, limits); !errors.Is(err, ErrInvalidProfileCiphertext) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestEncryptedArchiveHonorsLimitsAndCancellation(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "profile.zip")
	if err := os.WriteFile(source, bytes.Repeat([]byte("x"), 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	copy(key[:], testEncryptionKey)
	if _, _, err := encryptArchive(context.Background(), source, key, Limits{MaxArchiveBytes: 128}); !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("limit error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := encryptArchive(ctx, source, key, Limits{MaxArchiveBytes: 4096}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
