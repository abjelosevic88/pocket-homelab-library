package importers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// FernetDecrypt opens a Fernet token (what Calibre-Web seals its mail
// password with; the key is in the .key file next to app.db). The token's
// age is not checked.
func FernetDecrypt(key, token string) ([]byte, error) {
	k, err := b64url(strings.TrimSpace(key))
	if err != nil || len(k) != 32 {
		return nil, errors.New("fernet: the key is not 32 bytes of base64")
	}
	t, err := b64url(strings.TrimSpace(token))
	if err != nil {
		return nil, errors.New("fernet: the token is not base64")
	}
	// 0x80 | timestamp (8) | IV (16) | ciphertext (n*16) | HMAC (32)
	if len(t) < 1+8+16+16+32 || t[0] != 0x80 || (len(t)-1-8-16-32)%aes.BlockSize != 0 {
		return nil, errors.New("fernet: malformed token")
	}
	signing, encryption := k[:16], k[16:]
	body, sum := t[:len(t)-32], t[len(t)-32:]
	mac := hmac.New(sha256.New, signing)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sum) {
		return nil, errors.New("fernet: wrong key (the signature does not match)")
	}
	block, err := aes.NewCipher(encryption)
	if err != nil {
		return nil, err
	}
	iv, ct := body[9:25], body[25:]
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("fernet: bad padding")
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, errors.New("fernet: bad padding")
		}
	}
	return out[:len(out)-pad], nil
}

func b64url(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}
