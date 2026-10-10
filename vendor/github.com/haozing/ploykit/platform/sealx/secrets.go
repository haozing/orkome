// Secrets：面向业务域的租户凭据静态加密端口实现（能力计划 J）。
// Seal 落库前调用；Unseal 服务端读取处调用。
// 兼容明文读已于 2026-10-07 移除（维护者裁定不留旧数据兼容）：
// 无 SealedPrefix 前缀的行一律报错（ErrPlaintext，提示重配凭据），不再透传。
package sealx

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"strings"
)

// SealedPrefix 是密文判别前缀：sealed:v1:<base64(nonce||ciphertext)>。
// v1 为密文格式版本；无此前缀即明文（拒绝读取）。
// 轮换路径（SL2，触发=多实例部署/合规审计时实施，对标 Django SECRET_KEY_FALLBACKS
// 与 Grafana re-encrypt）：新增 v2 版本位双读（Unseal 按前缀选钥）+ 重加密迁移命令；
// 版本号在密文前缀里，即天然的轮换槽。登记处：docs/backlog.md。
const SealedPrefix = "sealed:v1:"

// EnvKey 是主密钥环境变量名（base64 of 32 bytes，AES-256）。
const EnvKey = "PLOYKIT_SEAL_KEY"

// ErrNoKey 是未配置主密钥（None 形态）时 Seal/Unseal(密文行) 的固定错误。
var ErrNoKey = errors.New("sealx: seal key not configured (set " + EnvKey + "); refusing to store plaintext")

// ErrPlaintext 是无密文前缀（明文行）时 Unseal 的固定错误——兼容明文读
// 移除后，明文行的唯一出路是重配凭据（写入侧 Seal）。
var ErrPlaintext = errors.New("sealx: value is not sealed (no " + SealedPrefix + " prefix); legacy plaintext support removed 2026-10-07 — reconfigure the credential")

// Secrets 实现 Seal/Unseal 端口；nil 接收者与未配 key 等价（None 形态）：
// Seal 一律报错（杜绝静默明文新写入），Unseal 对明文行/无 key 的密文行均报错。
// 方法全部 nil-safe，NewSecretsFromEnv 的 nil 返回值可直接注入。
type Secrets struct{ sealer *Sealer }

// NewSecrets 用 32 字节主密钥构造（AES-256-GCM，复用 Sealer）。
func NewSecrets(key []byte) (*Secrets, error) {
	s, err := New(key)
	if err != nil {
		return nil, err
	}
	return &Secrets{sealer: s}, nil
}

// NewSecretsFromEnv 读 EnvKey（base64 of 32 bytes）：
//   - 未设置 → 记警告并返回 (nil, nil)——None 形态（启动告警不阻断）；
//   - base64 / 长度非法 → 返回错误——坏配置应启动即失败，不得静默降级。
func NewSecretsFromEnv() (*Secrets, error) {
	v := strings.TrimSpace(os.Getenv(EnvKey))
	if v == "" {
		slog.Warn("sealx: "+EnvKey+" not set; sealed writes will be rejected and unsealed reads will fail", "prefix", SealedPrefix)
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, errors.New("sealx: " + EnvKey + " is not valid base64: " + err.Error())
	}
	return NewSecrets(key)
}

// Seal 加密并加 SealedPrefix 前缀；None 形态一律 ErrNoKey。
func (s *Secrets) Seal(plain string) (string, error) {
	if s == nil || s.sealer == nil {
		return "", ErrNoKey
	}
	blob, err := s.sealer.Encrypt(plain)
	if err != nil {
		return "", err
	}
	return SealedPrefix + base64.StdEncoding.EncodeToString(blob), nil
}

// Unseal 解密：无 SealedPrefix 前缀 → ErrPlaintext（兼容明文读已移除，
// 2026-10-07）；前缀命中 → 解密；失败（多为密钥不符或密文损坏）记警告并
// 返回空串+错误。
func (s *Secrets) Unseal(stored string) (string, error) {
	if !strings.HasPrefix(stored, SealedPrefix) {
		return "", ErrPlaintext
	}
	if s == nil || s.sealer == nil {
		slog.Warn("sealx: sealed value found but no seal key configured")
		return "", ErrNoKey
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, SealedPrefix))
	if err != nil {
		slog.Warn("sealx: sealed value is not valid base64")
		return "", errors.New("sealx: sealed value malformed")
	}
	plain, err := s.sealer.Decrypt(blob)
	if err != nil {
		slog.Warn("sealx: unseal failed (key mismatch or corrupted ciphertext)")
		return "", err
	}
	return plain, nil
}
