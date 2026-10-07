package upstream

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func cacheKey(t *testing.T, b []byte) string {
	t.Helper()
	var m map[string]any
	if e := json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	s, _ := m["prompt_cache_key"].(string)
	return s
}
func TestV2CacheKey(t *testing.T) {
	c := &Client{CacheSecret: bytes.Repeat([]byte{1}, 32)}
	raw := []byte(`{"model":"m","seed":9007199254740993}`)
	a := c.injectCacheKey(raw, "cn", "uid", "conv", "principal")
	if !bytes.Contains(a, []byte("9007199254740993")) {
		t.Fatal(string(a))
	}
	key := cacheKey(t, a)
	if key == "" {
		t.Fatal("missing key")
	}
	if cacheKey(t, c.injectCacheKey(raw, "cn", "uid", "conv", "principal")) != key {
		t.Fatal("unstable")
	}
	for _, b := range [][]byte{c.injectCacheKey(raw, "global", "uid", "conv", "principal"), c.injectCacheKey(raw, "cn", "other", "conv", "principal"), c.injectCacheKey(raw, "cn", "uid", "other", "principal"), c.injectCacheKey(raw, "cn", "uid", "conv", "other")} {
		if cacheKey(t, b) == key {
			t.Fatal("namespace collision")
		}
	}
	if cacheKey(t, c.injectCacheKey(raw, "cn", "uid", "", "p")) != "" {
		t.Fatal("guessed conversation")
	}
	explicit := []byte(`{"model":"m","prompt_cache_key":"explicit"}`)
	if !bytes.Equal(explicit, c.injectCacheKey(explicit, "cn", "uid", "conv", "p")) {
		t.Fatal("explicit changed")
	}
}
func TestV2CacheSecret(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	a, e := LoadCacheSecret(p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := LoadCacheSecret(p)
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("secret changed", e)
	}
}
