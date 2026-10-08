package upstream

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/linguo2625469/workbuddy2api-panel/internal/jsondoc"
)

// LoadCacheSecret creates an account-independent persistent HMAC secret atomically.
func LoadCacheSecret(path string) ([]byte, error) {
	read := func() ([]byte, error) {
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if len(b) != 32 {
			return nil, fmt.Errorf("cache secret must contain exactly 32 bytes")
		}
		return b, nil
	}
	if b, e := read(); e == nil {
		return b, nil
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		return read()
	}
	if e != nil {
		return nil, e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return nil, e
	}
	if ce != nil {
		return nil, ce
	}
	return b, nil
}

// injectCacheKey 注入 prompt_cache_key（字节入口，语义见 injectCacheKeyObject）。
// 解析失败或无需注入时原样返回（尽量不动调用方字节）。
func (c *Client) injectCacheKey(body []byte, aRealm, uid, conversation, principal string) []byte {
	if len(c.CacheSecret) < 32 || conversation == "" {
		return body
	}
	obj, err := jsondoc.Object(body)
	if err != nil {
		return body
	}
	if !c.injectCacheKeyObject(obj, aRealm, uid, conversation, principal) {
		return body
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// injectCacheKeyObject 就地注入 prompt_cache_key（obj 必须是调用方拥有的可变副本），
// 返回是否注入。客户端自带该键时保留原值（不覆盖）。
//
// 拆出对象版本的目的：缓存键必须在**线格式转换之后**计算（材料含最终 model）且需要
// 在序列化之前写入，把它合并进同一次序列化就省掉一整遍解析 + marshal。
func (c *Client) injectCacheKeyObject(obj map[string]any, aRealm, uid, conversation, principal string) bool {
	if len(c.CacheSecret) < 32 || conversation == "" || obj == nil {
		return false
	}
	if _, present := obj["prompt_cache_key"]; present {
		return false
	}
	p := c.Profiles[aRealm]
	profile, _ := json.Marshal(p)
	material, _ := json.Marshal([]any{principal, aRealm, uid, obj["model"], string(profile), conversation})
	mac := hmac.New(sha256.New, c.CacheSecret)
	_, _ = mac.Write(material)
	obj["prompt_cache_key"] = "wb2-" + hex.EncodeToString(mac.Sum(nil))
	return true
}
