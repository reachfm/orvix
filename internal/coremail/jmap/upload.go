package jmap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxUploadBytes     int64 = 25 * 1024 * 1024
	maxMultipartBytes  int64 = maxUploadBytes + 1024*1024
	uploadCopyBufBytes       = 32 * 1024
)

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	username, mailboxID, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = username

	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := uploadedFilePart(w, r)
	if err != nil {
		if err == errUploadTooLarge {
			http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, `{"error":"file field required"}`, http.StatusBadRequest)
		}
		return
	}
	defer file.Close()

	blobID := generateUploadID()
	filename := sanitizeUploadFilename(header.Filename)
	if filename == "" {
		filename = "upload"
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadDir := filepath.Join(s.uploadBasePath(), fmt.Sprintf("%d", mailboxID))
	if err := os.MkdirAll(uploadDir, 0750); err != nil {
		http.Error(w, `{"error":"storage error"}`, http.StatusInternalServerError)
		return
	}
	uploadPath := filepath.Join(uploadDir, fmt.Sprintf("%s_%s", blobID, sanitizeUploadFilename(filename)))
	size, err := streamUploadToFile(file, uploadPath)
	if err != nil {
		os.Remove(uploadPath)
		if err == errUploadTooLarge {
			http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, `{"error":"write error"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"accountId": fmt.Sprintf("%d", mailboxID),
		"blobId":    blobID,
		"size":      size,
		"type":      contentType,
		"name":      filename,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

var errUploadTooLarge = fmt.Errorf("upload too large")

type uploadedPart interface {
	io.Reader
	io.Closer
}

func uploadedFilePart(w http.ResponseWriter, r *http.Request) (uploadedPart, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return nil, nil, errUploadTooLarge
		}
		return nil, nil, err
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, nil, fmt.Errorf("file field required")
		}
		if err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				return nil, nil, errUploadTooLarge
			}
			return nil, nil, err
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		return part, &multipart.FileHeader{
			Filename: part.FileName(),
			Header:   part.Header,
		}, nil
	}
}

func streamUploadToFile(src io.Reader, path string) (int64, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	limited := io.LimitReader(src, maxUploadBytes+1)
	buf := make([]byte, uploadCopyBufBytes)
	size, err := io.CopyBuffer(out, limited, buf)
	if err != nil {
		return size, err
	}
	if size > maxUploadBytes {
		return size, errUploadTooLarge
	}
	return size, nil
}

func (s *Server) uploadBasePath() string {
	if s.MailStore != nil {
		return filepath.Join(s.MailStore.BasePath, "uploads")
	}
	return filepath.Join(os.TempDir(), "orvix-uploads")
}

func generateUploadID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeUploadFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	var clean []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			clean = append(clean, c)
		} else {
			clean = append(clean, '_')
		}
	}
	return string(clean)
}

func (s *Server) consumeUploadBlob(mailboxID uint, blobID string) ([]byte, string, string, error) {
	uploadDir := filepath.Join(s.uploadBasePath(), fmt.Sprintf("%d", mailboxID))
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		return nil, "", "", fmt.Errorf("upload not found")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), blobID+"_") {
			path := filepath.Join(uploadDir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, "", "", err
			}
			os.Remove(path)
			name := strings.TrimPrefix(entry.Name(), blobID+"_")
			return data, name, "", nil
		}
	}
	return nil, "", "", fmt.Errorf("upload blob not found: %s", blobID)
}
