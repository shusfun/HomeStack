package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

func randomCryptoToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return "", fmt.Errorf("生成 OIDC 安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
