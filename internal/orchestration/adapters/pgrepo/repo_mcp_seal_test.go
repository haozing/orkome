package pgrepo

// W2-3 / FP-09 P1-3：MCP 凭证密封链的纯单元测试（不依赖 DB）。
// sealCreds/unsealCreds 是包级函数，用内存假 Seal 断言加密往返与
// fail-closed 矩阵；SQL 级行为（信封落库、coalesce 三态、23505→409）
// 见 repo_mcp_creds_integration_test.go（TEST_DATABASE_URL 门控）。

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

// fakeSeal：内存假 sealx——密文形如 "enc:<plain>"，解密时校验前缀，
// 非 "enc:" 前缀即报错（模拟主钥不匹配/密文损坏）。
type fakeSeal struct{ calls int }

func (f *fakeSeal) Encrypt(p string) ([]byte, error) {
	f.calls++
	return append([]byte("enc:"), []byte(p)...), nil
}

func (f *fakeSeal) Decrypt(b []byte) (string, error) {
	f.calls++
	s := string(b)
	if !strings.HasPrefix(s, "enc:") {
		return "", errors.New("fakeSeal: not a fake-sealed blob")
	}
	return strings.TrimPrefix(s, "enc:"), nil
}

func TestSealCreds_Roundtrip(t *testing.T) {
	seal := &fakeSeal{}
	creds := map[string]any{"headers": map[string]any{"Authorization": "Bearer sk-secret-1"}}

	blob, err := sealCreds(seal, creds)
	require.NoError(t, err)

	// 落库形状：jsonb 信封 {"sealed":"<base64>"}，不含任何明文痕迹。
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(blob, &envelope))
	require.Len(t, envelope, 1, "信封只含 sealed 一个键")
	var b64 string
	require.NoError(t, json.Unmarshal(envelope["sealed"], &b64))
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	assert.Equal(t, "enc:", string(raw[:4]), "密文确经 Seal.Encrypt")

	assert.NotContains(t, string(blob), "sk-secret-1", "库里见不到明文")
	assert.NotContains(t, string(blob), "Authorization", "键名也进密文")

	got, err := unsealCreds(seal, blob)
	require.NoError(t, err)
	assert.Equal(t, creds, got, "解密往返还原明文 map")
	assert.Equal(t, 2, seal.calls, "一封一拆")
}

func TestSealCreds_EmptyNeverSealed(t *testing.T) {
	seal := &fakeSeal{}
	for name, c := range map[string]map[string]any{"nil": nil, "empty": {}} {
		blob, err := sealCreds(seal, c)
		require.NoError(t, err, name)
		assert.JSONEq(t, `{}`, string(blob), name+" 恒落 '{}'（空凭证无需密封）")
	}
	assert.Zero(t, seal.calls, "空凭证不触碰 Seal")
	// 空 Seal（未接线）写空凭证也必须放行——fail-closed 只针对非空凭证。
	blob, err := sealCreds(nil, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(blob))
}

func TestSealCreds_NilSealerFailClosed(t *testing.T) {
	_, err := sealCreds(nil, map[string]any{"api_key": "sk-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-closed", "Seal 未接线写非空凭证直接拒绝（对照 CreateCredential 先例）")
}

func TestUnsealCreds_Matrix(t *testing.T) {
	seal := &fakeSeal{}
	valid, err := sealCreds(seal, map[string]any{"api_key": "sk-2"})
	require.NoError(t, err)

	t.Run("empty bytes and empty json mean no credentials", func(t *testing.T) {
		got, err := unsealCreds(seal, nil)
		require.NoError(t, err)
		assert.Nil(t, got)
		got, err = unsealCreds(seal, []byte(`{}`))
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("legacy plaintext rejected fail-closed", func(t *testing.T) {
		// 迁移 1038 之前的存量形状：明文 jsonb。读路径必须拒绝，绝不回落。
		for _, legacy := range []string{`{"api_key":"sk-legacy"}`, `{"headers":{"Authorization":"Bearer x"}}`} {
			_, err := unsealCreds(seal, []byte(legacy))
			require.Error(t, err, legacy)
			assert.Contains(t, err.Error(), "legacy plaintext rejected")
		}
	})

	t.Run("non-envelope json rejected", func(t *testing.T) {
		_, err := unsealCreds(seal, []byte(`{"foo":"bar"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "legacy plaintext rejected")
		_, err = unsealCreds(seal, []byte(`not-json`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a sealed envelope")
	})

	t.Run("valid envelope without sealer rejected", func(t *testing.T) {
		_, err := unsealCreds(nil, valid)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not wired")
	})

	t.Run("malformed envelope base64 rejected", func(t *testing.T) {
		_, err := unsealCreds(seal, []byte(`{"sealed":"@@not-base64@@"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed")
	})

	t.Run("undecryptable ciphertext rejected", func(t *testing.T) {
		bad := base64.StdEncoding.EncodeToString([]byte("garbage-without-prefix"))
		_, err := unsealCreds(seal, []byte(`{"sealed":"`+bad+`"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unseal failed", "主钥不匹配/密文损坏 → 拒绝该服务器集成")
	})
}

func TestIsDupName_Maps23505Only(t *testing.T) {
	assert.True(t, isDupName(&pgconn.PgError{Code: "23505"}), "唯一约束冲突要被识别")
	for _, code := range []string{"23514", "23503", "42P01"} {
		assert.False(t, isDupName(&pgconn.PgError{Code: code}), code+" 不是重名")
	}
	assert.False(t, isDupName(nil))
	assert.False(t, isDupName(errors.New("plain error")))

	// 统一出口：errMCPTaken 必须是 409 语义（E_REVISION_CONFLICT）。
	var apiErr *apierr.Error
	require.ErrorAs(t, errMCPTaken, &apiErr)
	assert.Equal(t, apierr.Conflict, apiErr.Code)
	assert.Equal(t, "mcp server name already taken", apiErr.Message)
}
