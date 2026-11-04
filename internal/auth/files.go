package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LoadSecret 读取 HMAC 密钥文件；不存在时生成 32 字节并写入 0600。
// 密钥只从文件来，不接受配置里的明文或环境变量。
func LoadSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("auth: secret_file 不能为空")
	}
	if err := checkPerm(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err == nil {
		secret, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(secret) < 16 {
			return nil, fmt.Errorf("密钥文件 %s 不是合法 hex（至少 16 字节）", path)
		}
		return secret, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("密钥文件 %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(buf)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return buf, nil
}

// LoadEpoch 读取会话 epoch；不存在时生成。
// epoch 一变，所有已签发的会话与分享码立即失效。
func LoadEpoch(path string) (string, error) {
	if err := checkPerm(path); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err == nil {
		v := strings.TrimSpace(string(b))
		if v == "" {
			return "", fmt.Errorf("epoch 文件 %s 为空", path)
		}
		return v, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("epoch 文件 %s: %w", path, err)
	}
	return RotateEpoch(path)
}

// RotateEpoch 生成新的 epoch 并覆盖写入（0600）。
func RotateEpoch(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	v := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(v+"\n"), 0o600); err != nil {
		return "", err
	}
	return v, nil
}

// permEnforced 报告当前平台是否支持 Unix 权限位。Windows 用 ACL 控制访问，
// Stat 返回的 mode 只是只读属性的映射（总是 0666 或 0444），检查它没有意义。
const permEnforced = runtime.GOOS != "windows"

// checkPerm 拒绝其他用户可读写的密钥文件。文件不存在不算错。
func checkPerm(path string) error {
	if !permEnforced {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if perm := st.Mode().Perm(); perm&0o007 != 0 {
		return fmt.Errorf("密钥文件 %s 权限为 %04o，其他用户可访问；请 chmod 600", path, perm)
	}
	return nil
}
