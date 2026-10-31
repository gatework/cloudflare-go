package credentials

import (
	"errors"
	"github.com/gatework/cloudflare-go/internal/fileio"
	"os"
	"strings"
	"unicode"
)

func Validate(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 8192 || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("Cloudflare Token 为空或格式无效")
	}
	return token, nil
}
func Read(file, environment string) (string, error) {
	if file != "" {
		data, err := fileio.ReadRegularFile(file, 8192)
		if err != nil {
			return "", errors.New("无法读取 Token 文件，请检查路径和权限")
		}
		return Validate(string(data))
	}
	if environment == "" {
		environment = "CLOUDFLARE_API_TOKEN"
	}
	return Validate(os.Getenv(environment))
}
