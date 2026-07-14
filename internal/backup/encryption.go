package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	backupEnvelopeMagic = "ORVBK01\n"
	backupChunkSize     = 4 * 1024 * 1024
	maxEncryptedChunk   = backupChunkSize + 64
)

// LoadBackupEncryptionKey reads a 32-byte key from a root-owned key file.
// Hex and unpadded URL-safe base64 are accepted to keep installer-generated
// key files operator-friendly. Raw 32-byte files are also accepted.
func LoadBackupEncryptionKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat backup encryption key: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("backup encryption key permissions must allow at most owner read/write and group read")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup encryption key: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	for _, decode := range []func(string) ([]byte, error){
		hex.DecodeString,
		base64.RawURLEncoding.DecodeString,
		base64.StdEncoding.DecodeString,
	} {
		if key, decodeErr := decode(trimmed); decodeErr == nil && len(key) == 32 {
			return append([]byte(nil), key...), nil
		}
	}
	if len(data) == 32 {
		return append([]byte(nil), data...), nil
	}
	return nil, fmt.Errorf("backup encryption key must decode to exactly 32 bytes")
}

func validateBackupKey(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("backup encryption key must be exactly 32 bytes")
	}
	return nil
}

func backupAEAD(key []byte) (cipher.AEAD, error) {
	if err := validateBackupKey(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create backup cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create backup AEAD: %w", err)
	}
	return aead, nil
}

func chunkAAD(index uint64) []byte {
	aad := make([]byte, len(backupEnvelopeMagic)+8)
	copy(aad, backupEnvelopeMagic)
	binary.BigEndian.PutUint64(aad[len(backupEnvelopeMagic):], index)
	return aad
}

// EncryptBackupFile writes a chunked AES-256-GCM envelope. Each chunk has an
// independent nonce and authenticated sequence number, so large backups are
// streamed with bounded memory while truncation, reordering, and tampering all
// fail closed during decryption.
func EncryptBackupFile(key []byte, sourcePath, destinationPath string) error {
	aead, err := backupAEAD(key)
	if err != nil {
		return err
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open plaintext backup: %w", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o750); err != nil {
		return fmt.Errorf("create encrypted backup directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destinationPath), ".orvix-backup-encrypt-*")
	if err != nil {
		return fmt.Errorf("create encrypted backup temp file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure encrypted backup temp file: %w", err)
	}
	w := bufio.NewWriterSize(tmp, 256*1024)
	if _, err := io.WriteString(w, backupEnvelopeMagic); err != nil {
		return fmt.Errorf("write backup envelope header: %w", err)
	}

	buf := make([]byte, backupChunkSize)
	var index uint64
	for {
		n, readErr := io.ReadFull(in, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("read plaintext backup: %w", readErr)
		}
		if n == 0 {
			break
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return fmt.Errorf("generate backup nonce: %w", err)
		}
		sealed := aead.Seal(nil, nonce, buf[:n], chunkAAD(index))
		if _, err := w.Write(nonce); err != nil {
			return fmt.Errorf("write backup nonce: %w", err)
		}
		if err := binary.Write(w, binary.BigEndian, uint32(len(sealed))); err != nil {
			return fmt.Errorf("write encrypted chunk size: %w", err)
		}
		if _, err := w.Write(sealed); err != nil {
			return fmt.Errorf("write encrypted backup chunk: %w", err)
		}
		index++
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}
	// The record format always starts with a nonce, including the terminal
	// record. This makes truncation at a chunk boundary distinguishable from a
	// complete envelope without introducing a special short record.
	if _, err := w.Write(make([]byte, aead.NonceSize())); err != nil {
		return fmt.Errorf("write backup envelope terminator nonce: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(0)); err != nil {
		return fmt.Errorf("write backup envelope terminator: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush encrypted backup: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync encrypted backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close encrypted backup: %w", err)
	}
	if err := os.Rename(tmpPath, destinationPath); err != nil {
		return fmt.Errorf("activate encrypted backup: %w", err)
	}
	committed = true
	return nil
}

// DecryptBackupFile authenticates and streams an encrypted backup into a
// restrictive temporary destination. The destination is removed on failure.
func DecryptBackupFile(key []byte, sourcePath, destinationPath string) error {
	aead, err := backupAEAD(key)
	if err != nil {
		return err
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open encrypted backup: %w", err)
	}
	defer in.Close()
	r := bufio.NewReaderSize(in, 256*1024)
	header := make([]byte, len(backupEnvelopeMagic))
	if _, err := io.ReadFull(r, header); err != nil || string(header) != backupEnvelopeMagic {
		return fmt.Errorf("invalid encrypted backup envelope")
	}

	out, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create decrypted backup temp file: %w", err)
	}
	complete := false
	defer func() {
		_ = out.Close()
		if !complete {
			_ = os.Remove(destinationPath)
		}
	}()
	var index uint64
	for {
		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(r, nonce); err != nil {
			return fmt.Errorf("encrypted backup is truncated")
		}
		var sealedLen uint32
		if err := binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
			return fmt.Errorf("encrypted backup is truncated")
		}
		if sealedLen == 0 {
			if _, err := r.Peek(1); !errors.Is(err, io.EOF) {
				return fmt.Errorf("encrypted backup has trailing data")
			}
			break
		}
		if sealedLen > maxEncryptedChunk {
			return fmt.Errorf("encrypted backup chunk exceeds limit")
		}
		sealed := make([]byte, sealedLen)
		if _, err := io.ReadFull(r, sealed); err != nil {
			return fmt.Errorf("encrypted backup is truncated")
		}
		plain, err := aead.Open(nil, nonce, sealed, chunkAAD(index))
		if err != nil {
			return fmt.Errorf("encrypted backup authentication failed")
		}
		if _, err := out.Write(plain); err != nil {
			return fmt.Errorf("write decrypted backup: %w", err)
		}
		index++
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync decrypted backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close decrypted backup: %w", err)
	}
	complete = true
	return nil
}
