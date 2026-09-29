package profilesync

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	encryptedProfileMagic = "ANTPSE01"
	encryptionChunkSize   = uint32(4 << 20)
	encryptionSaltSize    = 16
	encryptionKeyIDSize   = 16
	encryptedHeaderSize   = len(encryptedProfileMagic) + 4 + encryptionSaltSize + encryptionKeyIDSize
)

var ErrInvalidProfileCiphertext = errors.New("profile ciphertext is invalid")

// encryptArchive writes a bounded, streaming AES-256-GCM container. Each
// chunk and the terminal marker are independently authenticated. A random
// per-object salt feeds deterministic nonce derivation, so the same key can
// safely encrypt many revisions without loading the profile into memory.
func encryptArchive(ctx context.Context, sourcePath string, key [32]byte, limits Limits) (resultPath string, summary archiveSummary, err error) {
	limits = limits.withDefaults()
	input, err := os.Open(sourcePath)
	if err != nil {
		return "", archiveSummary{}, err
	}
	defer input.Close()

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", archiveSummary{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", archiveSummary{}, err
	}
	header := make([]byte, encryptedHeaderSize)
	copy(header, encryptedProfileMagic)
	binary.BigEndian.PutUint32(header[len(encryptedProfileMagic):], encryptionChunkSize)
	saltStart := len(encryptedProfileMagic) + 4
	salt := header[saltStart : saltStart+encryptionSaltSize]
	if _, err := rand.Read(salt); err != nil {
		return "", archiveSummary{}, fmt.Errorf("generate profile encryption salt: %w", err)
	}
	keyDigest := sha256.Sum256(key[:])
	copy(header[saltStart+encryptionSaltSize:], keyDigest[:encryptionKeyIDSize])

	output, err := os.CreateTemp("", "ant-profile-sync-*.enc")
	if err != nil {
		return "", archiveSummary{}, err
	}
	resultPath = output.Name()
	remove := true
	defer func() {
		if err != nil {
			_ = output.Close()
		}
		if remove {
			_ = os.Remove(resultPath)
		}
	}()
	hash := sha256.New()
	writer := &limitedWriter{w: io.MultiWriter(output, hash), max: limits.MaxArchiveBytes}
	if err = writeFull(writer, header); err != nil {
		return "", archiveSummary{}, err
	}

	buffer := make([]byte, int(encryptionChunkSize))
	var counter uint32
	for {
		if err = ctx.Err(); err != nil {
			return "", archiveSummary{}, err
		}
		n, readErr := io.ReadFull(input, buffer)
		if n > 0 {
			if err = writeEncryptedRecord(writer, aead, key, header, salt, counter, buffer[:n]); err != nil {
				return "", archiveSummary{}, err
			}
			if counter == math.MaxUint32 {
				return "", archiveSummary{}, ErrArchiveLimit
			}
			counter++
		}
		switch readErr {
		case nil:
			continue
		case io.EOF, io.ErrUnexpectedEOF:
			if err = writeEncryptedRecord(writer, aead, key, header, salt, counter, nil); err != nil {
				return "", archiveSummary{}, err
			}
		default:
			return "", archiveSummary{}, readErr
		}
		break
	}
	if err = output.Sync(); err != nil {
		return "", archiveSummary{}, err
	}
	if err = output.Close(); err != nil {
		return "", archiveSummary{}, err
	}
	remove = false
	return resultPath, archiveSummary{Size: writer.written, Hash: hex.EncodeToString(hash.Sum(nil))}, nil
}

func writeEncryptedRecord(writer io.Writer, aead cipher.AEAD, key [32]byte, header, salt []byte, counter uint32, plaintext []byte) error {
	if len(plaintext) > int(encryptionChunkSize) {
		return ErrArchiveLimit
	}
	length := uint32(len(plaintext))
	var lengthBytes [4]byte
	binary.BigEndian.PutUint32(lengthBytes[:], length)
	nonce := profileNonce(key, salt, counter)
	aad := profileAAD(header, counter, length)
	sealed := aead.Seal(nil, nonce[:], plaintext, aad)
	if err := writeFull(writer, lengthBytes[:]); err != nil {
		return err
	}
	return writeFull(writer, sealed)
}

func decryptArchive(ctx context.Context, encryptedPath string, key [32]byte, limits Limits) (resultPath string, err error) {
	limits = limits.withDefaults()
	stat, err := os.Stat(encryptedPath)
	if err != nil {
		return "", err
	}
	minimumSize := int64(encryptedHeaderSize + 4 + 16)
	if stat.Size() < minimumSize || stat.Size() > limits.MaxArchiveBytes {
		return "", ErrInvalidProfileCiphertext
	}
	input, err := os.Open(encryptedPath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	header := make([]byte, encryptedHeaderSize)
	if _, err := io.ReadFull(input, header); err != nil {
		return "", ErrInvalidProfileCiphertext
	}
	if string(header[:len(encryptedProfileMagic)]) != encryptedProfileMagic ||
		binary.BigEndian.Uint32(header[len(encryptedProfileMagic):]) != encryptionChunkSize {
		return "", ErrInvalidProfileCiphertext
	}
	saltStart := len(encryptedProfileMagic) + 4
	salt := header[saltStart : saltStart+encryptionSaltSize]
	keyDigest := sha256.Sum256(key[:])
	if subtle.ConstantTimeCompare(header[saltStart+encryptionSaltSize:], keyDigest[:encryptionKeyIDSize]) != 1 {
		return "", ErrInvalidProfileCiphertext
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	output, err := os.CreateTemp("", "ant-profile-sync-*.zip")
	if err != nil {
		return "", err
	}
	resultPath = output.Name()
	remove := true
	defer func() {
		if err != nil {
			_ = output.Close()
		}
		if remove {
			_ = os.Remove(resultPath)
		}
	}()
	writer := &limitedWriter{w: output, max: limits.MaxArchiveBytes}
	var counter uint32
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		var lengthBytes [4]byte
		if _, readErr := io.ReadFull(input, lengthBytes[:]); readErr != nil {
			return "", ErrInvalidProfileCiphertext
		}
		length := binary.BigEndian.Uint32(lengthBytes[:])
		if length > encryptionChunkSize {
			return "", ErrInvalidProfileCiphertext
		}
		sealed := make([]byte, int(length)+aead.Overhead())
		if _, readErr := io.ReadFull(input, sealed); readErr != nil {
			return "", ErrInvalidProfileCiphertext
		}
		nonce := profileNonce(key, salt, counter)
		plaintext, openErr := aead.Open(nil, nonce[:], sealed, profileAAD(header, counter, length))
		if openErr != nil || len(plaintext) != int(length) {
			return "", ErrInvalidProfileCiphertext
		}
		if length == 0 {
			var trailing [1]byte
			n, trailingErr := input.Read(trailing[:])
			if n != 0 || trailingErr != io.EOF {
				return "", ErrInvalidProfileCiphertext
			}
			break
		}
		if err = writeFull(writer, plaintext); err != nil {
			return "", err
		}
		if counter == math.MaxUint32 {
			return "", ErrInvalidProfileCiphertext
		}
		counter++
	}
	if err = output.Sync(); err != nil {
		return "", err
	}
	if err = output.Close(); err != nil {
		return "", err
	}
	remove = false
	return resultPath, nil
}

func profileNonce(key [32]byte, salt []byte, counter uint32) [12]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("ant-profile-sync-nonce-v1"))
	_, _ = mac.Write(salt)
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], counter)
	_, _ = mac.Write(encoded[:])
	digest := mac.Sum(nil)
	var nonce [12]byte
	copy(nonce[:], digest[:len(nonce)])
	return nonce
}

func profileAAD(header []byte, counter, length uint32) []byte {
	aad := make([]byte, len(header)+8)
	copy(aad, header)
	binary.BigEndian.PutUint32(aad[len(header):], counter)
	binary.BigEndian.PutUint32(aad[len(header)+4:], length)
	return aad
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
