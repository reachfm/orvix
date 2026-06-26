package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type WebPushSender struct {
	vapid  VAPIDConfig
	client *http.Client
}

func NewWebPushSender(vapid VAPIDConfig) *WebPushSender {
	return &WebPushSender{
		vapid:  vapid,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// NewWebPushSenderWithClient is the test seam: it lets the
// caller swap in a custom http.Client (typically wrapping an
// httptest.Server's transport) so the encryption / signing
// pipeline can be exercised without hitting a real push
// service. Production code uses NewWebPushSender.
func NewWebPushSenderWithClient(vapid VAPIDConfig, client *http.Client) *WebPushSender {
	if client == nil {
		return NewWebPushSender(vapid)
	}
	return &WebPushSender{vapid: vapid, client: client}
}

type vapidHeader struct {
	Typ string `json:"typ"`
	Alg string `json:"alg"`
}

type vapidClaims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

func (s *WebPushSender) Send(ctx context.Context, sub *PushSubscription, payload []byte) error {
	if sub.DisabledAt != nil || s.vapid.PrivateKey == "" {
		return nil
	}
	p256dh, err := base64.RawURLEncoding.DecodeString(sub.P256DHKey)
	if err != nil {
		return fmt.Errorf("decode p256dh: %w", err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(sub.AuthKey)
	if err != nil {
		return fmt.Errorf("decode auth: %w", err)
	}
	encrypted, salt, serverPub, err := encryptPayload(payload, p256dh, auth)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", sub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return fmt.Errorf("create push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Encryption", fmt.Sprintf("salt=%s", base64.RawURLEncoding.EncodeToString(salt)))
	req.Header.Set("Crypto-Key", fmt.Sprintf("dh=%s", base64.RawURLEncoding.EncodeToString(serverPub)))
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "normal")

	if s.vapid.PrivateKey != "" {
		vapidAuth, err := buildVAPIDHeader(s.vapid, sub.Endpoint)
		if err != nil {
			return fmt.Errorf("vapid auth: %w", err)
		}
		req.Header.Set("Authorization", "vapid t="+vapidAuth+", k="+s.vapid.PublicKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("push http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 429 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("push rejected: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func buildVAPIDHeader(vapid VAPIDConfig, endpoint string) (string, error) {
	header := vapidHeader{Typ: "JWT", Alg: "ES256"}
	headerJSON, _ := json.Marshal(header)
	aud := originFromEndpoint(endpoint)
	claims := vapidClaims{
		Aud: aud,
		Exp: time.Now().Add(12 * time.Hour).Unix(),
		Sub: vapid.Subject,
	}
	claimsJSON, _ := json.Marshal(claims)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	return signVAPID(headerB64, claimsB64, vapid.PrivateKey)
}

func originFromEndpoint(endpoint string) string {
	if idx := strings.Index(endpoint, "://"); idx > 0 {
		rest := endpoint[idx+3:]
		if slash := strings.IndexByte(rest, '/'); slash > 0 {
			return endpoint[:idx+3+slash]
		}
		return endpoint
	}
	return endpoint
}

func encryptPayload(payload, p256dhKey, authKey []byte) (ciphertext, salt, serverPub []byte, err error) {
	salt = make([]byte, 16)
	if _, err = io.ReadFull(rand.Reader, salt); err != nil {
		return nil, nil, nil, err
	}
	curve := elliptic.P256()
	localKey, err := ecdhGenerateKey(curve)
	if err != nil {
		return nil, nil, nil, err
	}
	localPub := elliptic.Marshal(curve, localKey.X, localKey.Y)
	x, y := elliptic.Unmarshal(curve, p256dhKey)
	if x == nil || y == nil {
		return nil, nil, nil, fmt.Errorf("invalid p256dh key")
	}
	sharedX, _ := curve.ScalarMult(x, y, localKey.D.Bytes())
	sharedSecret := sharedX.Bytes()

	info := append([]byte("WebPush: info\x00"), p256dhKey...)
	info = append(info, localPub...)
	ikm := ecdhExtract(salt, authKey)
	cekInfo := append([]byte("Content-Encoding: aes128gcm\x00"), info...)
	nonceInfo := append([]byte("Content-Encoding: nonce\x00"), info...)
	prk := ecdhExtract(sharedSecret, ikm)
	cek := expand(prk, cekInfo, 16)
	nonce := expand(prk, nonceInfo, 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, nil, nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	padded := payload
	paddingLen := 0
	if l := len(payload); l%16 != 0 {
		paddingLen = 16 - l%16
		padded = make([]byte, l+paddingLen+1)
		copy(padded, payload)
		padded[l+paddingLen] = byte(paddingLen)
	} else {
		padded = make([]byte, l+1)
		copy(padded, payload)
		padded[l] = 0
	}
	ciphertext = aesgcm.Seal(nil, nonce, padded, nil)
	return ciphertext, salt, localPub, nil
}

func ecdhExtract(secret, salt []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(secret)
	return mac.Sum(nil)
}

func expand(prk, info []byte, length int) []byte {
	var result []byte
	mac := hmac.New(sha256.New, prk)
	counter := byte(0)
	for len(result) < length {
		if counter > 0 {
			mac.Reset()
		}
		mac.Write(result)
		mac.Write(info)
		mac.Write([]byte{counter})
		result = mac.Sum(result)
		counter++
	}
	return result[:length]
}

func ecdhGenerateKey(curve elliptic.Curve) (*ecdhKey, error) {
	priv, x, y, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, err
	}
	return &ecdhKey{Priv: priv, X: x, Y: y, D: new(big.Int).SetBytes(priv)}, nil
}

type ecdhKey struct {
	Priv []byte
	X, Y *big.Int
	D    *big.Int
}
