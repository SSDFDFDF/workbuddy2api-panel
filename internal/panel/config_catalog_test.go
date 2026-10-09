// config_catalog_test.go 面板与配置域目录的一致性守护。
//
// 面板的「需重启」徽标由后端 internal/config/catalog.go 驱动（30-config.js 拉
// /panel/api/config/catalog 后按字段名注入）。这里钉住两件事：
//
//  1. CFG_MAP 里的每个配置路径都能在目录里查到生效方式——查不到说明目录漏登记
//     或路径写错，用户会看到"改了没提示"；
//  2. index.html 里不再有手写的「需重启」徽标——硬编码与目录两份名单必然漂移，
//     这正是 session_sticky.enabled 那个 bug 的温床。
package panel

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"workbuddy_manager/internal/config"
)

// cfgMapPaths 从 30-config.js 的 CFG_MAP 里解析「表单字段名 → 配置路径」。
func cfgMapPaths(t *testing.T) map[string]string {
	t.Helper()
	js := string(panelJS)
	i := strings.Index(js, "const CFG_MAP = {")
	if i < 0 {
		t.Fatal("30-config.js 里找不到 CFG_MAP")
	}
	block := js[i:]
	block = block[:strings.Index(block, "\n};")]

	out := map[string]string{}
	entry := regexp.MustCompile(`(?:^|[\s,{])([a-z_0-9]+):\s*\[([^\]]*)\]`)
	for _, m := range entry.FindAllStringSubmatch(block, -1) {
		name, list := m[1], m[2]
		var segs []string
		for _, s := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(list, -1) {
			segs = append(segs, s[1])
		}
		if len(segs) == 0 {
			t.Errorf("CFG_MAP 里 %s 的路径为空", name)
			continue
		}
		out[name] = strings.Join(segs, ".")
	}
	if len(out) == 0 {
		t.Fatal("未从 CFG_MAP 解析出任何条目")
	}
	return out
}

// TestConfigMapCoveredByCatalog CFG_MAP 的每个配置路径都要能在字段目录里查到。
//
// 这条守护把"面板能改的字段"与"目录声明的生效方式"绑在一起：目录漏登记时
// 面板拿不到 mode，徽标与保存提示都会失效（用户以为改了没用）。
func TestConfigMapCoveredByCatalog(t *testing.T) {
	cat := config.Entries()
	paths := cfgMapPaths(t)
	for name, path := range paths {
		if _, ok := cat.Lookup(path); !ok {
			t.Errorf("表单字段 %s（%s）在 internal/config 的字段目录里没有登记："+
				"面板拿不到它的生效方式，用户改了不会被提示", name, path)
		}
	}
}

// TestNoHardcodedRestartBadges 页面上不允许再出现手写的「需重启」徽标：
// 徽标必须由 catalog 驱动（后端说哪个字段需重启，前端才标哪个）。
func TestNoHardcodedRestartBadges(t *testing.T) {
	if n := strings.Count(string(indexHTML), "b-restart"); n != 0 {
		t.Errorf("index.html 仍有 %d 处手写的 b-restart 徽标：徽标必须由 /panel/api/config/catalog 驱动，"+
			"否则会和后端目录漂移（改动前请删掉手写徽标）", n)
	}
	// 前端必须真的去拉目录并按模式判定，而不是把名单又抄一份。
	js := string(panelJS)
	for _, need := range []string{
		"config/catalog",
		"function matchPath(",
		"function catalogEntry(",
		"function applyCatalogBadges(",
	} {
		if !strings.Contains(js, need) {
			t.Errorf("30-config.js 缺少 %q：徽标数据化链路断了", need)
		}
	}
	// 反向：前端不得自己列出"哪些字段需重启"的名单（出现即为第二份真相）。
	if regexp.MustCompile(`RESTART_FIELDS|restartFields\s*=`).MatchString(js) {
		t.Error("前端出现了自带的需重启名单：目录是唯一真相，不能在前端再抄一份")
	}
}

// TestCatalogBadgeRenderingIsIdempotent applyCatalogBadges 必须可重复调用：
// loadConfig 每次刷新都会调用它，重复追加会让徽标翻倍。
func TestCatalogBadgeRenderingIsIdempotent(t *testing.T) {
	js := string(panelJS)
	i := strings.Index(js, "function applyCatalogBadges()")
	if i < 0 {
		t.Fatal("找不到 applyCatalogBadges")
	}
	body := js[i:]
	body = body[:strings.Index(body, "\n}")]
	// 先查已有徽标再创建（幂等的写法）：必须出现 querySelector 与 remove。
	for _, need := range []string{"querySelector", "remove()"} {
		if !strings.Contains(body, need) {
			t.Errorf("applyCatalogBadges 缺少 %q：重复调用会叠加徽标", need)
		}
	}
}

// TestConfigAPIRoutesRequireAuth 配置域接口的挂载点必须带鉴权。
//
// 为什么单独守护：配置域 handler 由 panel 挂载（internal/config/api.go 自己不管
// 鉴权），漏掉 withAuth 就等于把"改配置"开成匿名接口——改 api_key/proxy/upstream
// 即可完全接管网关。这是本仓库最不该出现的一类回归，所以用测试钉住。
func TestConfigAPIRoutesRequireAuth(t *testing.T) {
	cfgAPI := config.NewAPI(config.APIConfig{
		ConfigPath: "/tmp/config.json",
		LoadConfig: func() (any, error) { return map[string]any{}, nil },
		SaveConfig: func([]byte) ([]string, error) { return nil, nil },
	})
	p := New(Config{Version: "test", APIKey: "test-key", ConfigAPI: cfgAPI})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/panel/api/config"},
		{"GET", "/panel/api/config/catalog"},
		{"POST", "/panel/api/config"},
		{"POST", "/panel/api/prompt/preview"},
	} {
		// 无 key → 401（而不是 200/404：404 说明根本没挂上，200 则更糟）。
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 无密钥: status=%d want 401（配置接口必须鉴权）", tc.method, tc.path, rec.Code)
		}
		// 带正确 key → 不是 401/404（进入 handler）。
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer test-key")
		rec = httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusNotFound {
			t.Errorf("%s %s 带正确密钥: status=%d（handler 未正确挂载）", tc.method, tc.path, rec.Code)
		}
	}
}
