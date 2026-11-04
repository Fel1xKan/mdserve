package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// argon2id 参数：32MiB 内存、3 轮、2 路并行。
// 每次校验约 40ms，配合 verifySem 限制并发，避免 8 个并发登录打爆内存。
const (
	argonMemory      = 32 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLen     = 16
	argonKeyLen      = 32
)

// verifySem 限制同时进行的内存硬哈希数量（2 × 32MiB）。
var verifySem = make(chan struct{}, 2)

// Credential 是某个 host 的口令来源。Hash 与 Plain 只有一个非空。
type Credential struct {
	Hash      string
	Plain     string
	Path      string
	Generated bool
}

// Describe 给启动横幅用，绝不返回口令本身。
func (c Credential) Describe() string {
	switch {
	case c.Generated:
		return "自动生成，已写入 " + c.Path
	case c.Hash != "":
		return "password_hash"
	case c.Path != "":
		return "password_file " + c.Path
	default:
		return "未设置"
	}
}

// Verify 校验口令。明文来源用常量时间比较，哈希来源走 argon2id/bcrypt。
func (c Credential) Verify(pw string) bool {
	if pw == "" {
		return false
	}
	if c.Hash != "" {
		return verifyHash(c.Hash, pw)
	}
	if c.Plain != "" {
		return subtle.ConstantTimeCompare([]byte(c.Plain), []byte(pw)) == 1
	}
	return false
}

// ResolveCredential 按 password_hash > password_file > 自动生成 的顺序取口令。
// 自动生成的文件落在 stateDir/passwords/<key>.pass，权限 0600，重启后复用。
func ResolveCredential(hash, file, stateDir, key string) (Credential, error) {
	if hash != "" {
		if err := validateHash(hash); err != nil {
			return Credential{}, err
		}
		return Credential{Hash: hash}, nil
	}
	if file != "" {
		return readCredential(file)
	}
	if stateDir == "" {
		return Credential{}, errors.New("auth: 既没有 password_hash/password_file，也没有可用的 state_dir")
	}
	path := filepath.Join(stateDir, "passwords", key+".pass")
	if _, err := os.Stat(path); err == nil {
		return readCredential(path)
	}
	pw := RandomPassword(20)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Credential{}, err
	}
	if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
		return Credential{}, err
	}
	return Credential{Plain: pw, Path: path, Generated: true}, nil
}

func readCredential(path string) (Credential, error) {
	st, err := os.Stat(path)
	if err != nil {
		return Credential{}, fmt.Errorf("口令文件 %s: %w", path, err)
	}
	if st.IsDir() {
		return Credential{}, fmt.Errorf("口令文件 %s 是目录", path)
	}
	if perm := st.Mode().Perm(); permEnforced && perm&0o007 != 0 {
		return Credential{}, fmt.Errorf("口令文件 %s 权限为 %04o，其他用户可访问；请 chmod 600", path, perm)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return Credential{}, fmt.Errorf("口令文件 %s 为空", path)
	}
	if looksLikeHash(v) {
		if err := validateHash(v); err != nil {
			return Credential{}, fmt.Errorf("口令文件 %s: %w", path, err)
		}
		return Credential{Hash: v, Path: path}, nil
	}
	return Credential{Plain: v, Path: path}, nil
}

func looksLikeHash(s string) bool {
	return strings.HasPrefix(s, "$argon2id$") || strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

// validateHash 在启动时就拒绝不认识的哈希，避免"配了但永远登不上"
// 或者更糟的"配了但被当成明文"。
func validateHash(s string) error {
	switch {
	case strings.HasPrefix(s, "$argon2id$"):
		if _, err := parseArgon2id(s); err != nil {
			return fmt.Errorf("password_hash: %w", err)
		}
		return nil
	case strings.HasPrefix(s, "$2a$"), strings.HasPrefix(s, "$2b$"), strings.HasPrefix(s, "$2y$"):
		if _, err := bcrypt.Cost([]byte(s)); err != nil {
			return fmt.Errorf("password_hash: bcrypt 格式错误: %w", err)
		}
		return nil
	default:
		return errors.New("password_hash: 只支持 argon2id 与 bcrypt；不接受 sha256")
	}
}

func verifyHash(encoded, pw string) bool {
	verifySem <- struct{}{}
	defer func() { <-verifySem }()

	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		p, err := parseArgon2id(encoded)
		if err != nil {
			return false
		}
		sum := argon2.IDKey([]byte(pw), p.salt, p.iterations, p.memory, p.parallelism, uint32(len(p.hash)))
		return subtle.ConstantTimeCompare(sum, p.hash) == 1
	case strings.HasPrefix(encoded, "$2"):
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(pw)) == nil
	default:
		return false
	}
}

type argonParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	hash        []byte
}

func parseArgon2id(encoded string) (*argonParams, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, errors.New("不是合法的 argon2id 哈希")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, fmt.Errorf("argon2 版本不支持: %q", parts[2])
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return nil, fmt.Errorf("argon2 参数无法解析: %q", parts[3])
	}
	if memory == 0 || iterations == 0 || parallelism == 0 {
		return nil, errors.New("argon2 参数不能为 0")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, fmt.Errorf("argon2 salt: %w", err)
	}
	sum, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, fmt.Errorf("argon2 hash: %w", err)
	}
	if len(salt) < 8 || len(sum) < 16 {
		return nil, errors.New("argon2 salt 或 hash 过短")
	}
	return &argonParams{memory: memory, iterations: iterations, parallelism: parallelism, salt: salt, hash: sum}, nil
}

// HashPassword 生成 argon2id 哈希，供 `mdserve passwd` 使用。
func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("口令至少 8 个字符")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(pw), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// RandomPassword 生成 n 位口令，字符集是 base32（无 0/O/1/l 混淆）。
func RandomPassword(n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(rand.Text())
	}
	return b.String()[:n]
}
