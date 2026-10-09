// load.go 从文件/JSON 加载配置、版本迁移、环境变量覆盖、首次运行生成推荐配置。
//
// 来源分层（优先级由低到高）：Default → File（含版本迁移）→ Env(WB2A_*)。
// -config 只决定读哪个文件，不参与字段覆盖。
//
// 读取路径（Load）：
//
//	读文件 → 识别 config_version → 迁移到 CurrentVersion → 立即回写（快照旧文件）
//	      → 解析 + 归一化 → env 覆盖
//
// 迁移与归一化的分工见 migrate.go 的文件头（纪律：normalize 只认当前版本）。
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"workbuddy_manager/internal/jsondoc"
	"workbuddy_manager/internal/upstream"
)

// runtimeMetaKeys 运行期元数据键：写在配置对象上供面板展示，但**不是配置输入**。
//
// 不把它们从文件里读进来（否则用户手写的旧值会污染当期告警列表），也不在回写时
// 持久化（迁移回写会重写整个文件，留着就会一直存在）。
var runtimeMetaKeys = []string{"_warnings", "_migrations"}

func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		// 目录检查：Docker bind mount 在宿主机文件缺失时会静默创建同名目录，
		// 直接 ReadFile 会报 "Incorrect function" 之类晦涩错误，这里给出可操作提示。
		if st, statErr := os.Stat(path); statErr == nil && st.IsDir() {
			return nil, fmt.Errorf("config %s 是目录而非文件——"+
				"Docker 部署时若宿主机缺少 config.json，bind mount 会创建同名目录。"+
				"请先 `cp config.example.json config.json` 或删除该目录（程序会自动生成配置）", path)
		}
		// 读 → 迁移 → 回写整段在配置文件事务锁里（与面板保存共用同一把锁，
		// 见 lock.go）：否则迁移回写可能覆盖掉同时发生的一次面板保存。
		var (
			obj      map[string]any
			raw      []byte
			rep      MigrateReport
			writeErr error
		)
		err := FileTx(func() error {
			var rerr error
			raw, rerr = os.ReadFile(path)
			if rerr != nil {
				return fmt.Errorf("read config: %w", rerr)
			}
			obj, rerr = jsondoc.Object(raw)
			if rerr != nil {
				return fmt.Errorf("parse config: %w", rerr)
			}
			// 版本迁移：旧形态在这里一次性转成当前版本，并立即回写磁盘。
			rep, rerr = Migrate(obj)
			if rerr != nil {
				return rerr
			}
			for _, k := range runtimeMetaKeys {
				delete(obj, k)
			}
			if rep.Migrated() {
				// 回写失败不阻断启动：内存里已是当前版本，功能不受影响。
				// 告警在 parseObject 之后追加（parseObject 会重置 Warnings）。
				writeErr = writeMigrated(path, raw, obj, rep)
			}
			// 首次成功加载顺手种一份兜底备份（已存在则不动）：把可用回滚点
			// 提前到"用户第一次改配置"之前（否则第一次改坏时磁盘上还没有任何备份）。
			seedBackupOnce(path)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if _, err := parseObject(obj, c); err != nil {
			return nil, err
		}
		if rep.Migrated() {
			c.Migrations = append(c.Migrations, rep.Steps...)
			c.Migrations = append(c.Migrations, rep.Notes...)
			if writeErr != nil {
				// 必须可见：只读挂载下每次启动都会重新迁移，用户得知道为什么。
				c.addWarning(fmt.Sprintf("配置已自动迁移到 v%d，但回写磁盘失败（%v）：本次迁移只在内存生效，重启后会重新迁移",
					rep.To, writeErr))
			}
		}
	}
	applyEnv(c)
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// writeMigrated 把迁移后的配置回写磁盘：先留一份迁移前快照（config.json.v<from>），
// 再原子替换。快照失败不阻断（best effort）——它只是给用户多一条回退路径。
func writeMigrated(path string, beforeRaw []byte, obj map[string]any, rep MigrateReport) error {
	out, err := MarshalConfig(obj)
	if err != nil {
		return err
	}
	_ = os.WriteFile(VersionSnapshotPath(path, rep.From), beforeRaw, 0o600)
	return WriteFileAtomic(path, out)
}

// seedBackupOnce 每个配置文件只尝试种一次 .bak（面板保存路径也会用到它）。
var seedBackupOnce = func() func(string) {
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(path string) {
		mu.Lock()
		defer mu.Unlock()
		if seen[path] {
			return
		}
		seen[path] = true
		if err := SeedBackup(path); err != nil {
			LogWarnings(&Config{Warnings: []string{"备份 .bak 写入失败（不影响运行）: " + err.Error()}})
		}
	}
}()

// parseObject 把已解析的配置对象覆盖到 c 上（不做 env、不读文件、不迁移）。
func parseObject(obj map[string]any, c *Config) (*Config, error) {
	if err := decodeInto(obj, c); err != nil {
		return nil, err
	}
	// 运行期元数据不是配置输入：文件里若残留 _warnings/_migrations（历史手写或
	// 旧实现写的），一律不读进来，否则会污染当期告警/迁移提示。
	c.Warnings = nil
	c.Migrations = nil
	// 版本已经在 Migrate 里归一：到了这里只可能是当前版本（或未经迁移的裸 JSON，
	// 见 ParseConfig 的版本闸门）。
	c.ConfigVersion = CurrentVersion
	// 未知键只告警不阻断启动（面板保存时直接丢弃，见 PruneUnknownKeys）。
	// 已知的历史键不该走到这里：它们由 migrate.go 一次性清理掉。
	for _, k := range unknownConfigKeys(obj) {
		c.addWarning(k + " —— 未知配置项，已忽略（检查拼写或升级版本）")
	}
	for realm, p := range c.Upstream.Profiles {
		if realm != "cn" && realm != "global" {
			c.addWarning("upstream.profiles." + realm + " —— 未知 realm，已忽略")
			delete(c.Upstream.Profiles, realm)
			continue
		}
		if err := upstream.ValidateIdentity(p); err != nil {
			return nil, err
		}
	}
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// MigrateMap 把可能过期的配置对象迁移到当前版本（就地改写），并返回迁移报告。
//
// 两条使用路径：
//   - Load：读文件后迁移 + 回写；
//   - 保存路径（cmd/server）：旧的磁盘内容与面板提交合并后，先迁移再校验/落盘，
//     保证"写下去的文件永远是当前版本"（否则每次启动都要迁移一遍）。
func MigrateMap(obj map[string]any) (MigrateReport, error) { return Migrate(obj) }

// addWarning 追加去重后的配置告警（normalize 会被重复调用，同一告警不重复展示）。
func (c *Config) addWarning(w string) {
	if w == "" {
		return
	}
	for _, existing := range c.Warnings {
		if existing == w {
			return
		}
	}
	c.Warnings = append(c.Warnings, w)
}

// decodeInto 把配置对象解码到 c 上，并执行**版本闸门**。
//
// 闸门规则：只接受当前版本或完全不带版本号的 JSON。旧版本的显式声明必须报错——
// 这正是"一次性迁移代替长期归一化"的执行点（旧形态不会走到 normalize）。
// 版本缺省（0）视为「未声明」：面板表单与单测都会写不带版本号的片段，它们天然是
// 当前形态；而**文件路径**（Load）已经先经过 Migrate，真实旧文件不会落到这里。
func decodeInto(obj map[string]any, c *Config) error {
	if err := json.Unmarshal(MergedJSON(obj), c); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	switch v := c.ConfigVersion; {
	case v == 0 || v == CurrentVersion:
		c.ConfigVersion = CurrentVersion
	case v > CurrentVersion:
		return fmt.Errorf("config_version: %d 由更新版本的程序写入（本程序支持到 %d）：请用新版程序启动", v, CurrentVersion)
	default:
		return fmt.Errorf("config_version: %d 是旧版本，需要一次性迁移：请通过配置文件启动（Load 会自动迁移并回写）或先调用 MigrateMap", v)
	}
	return nil
}

// ParseConfig 基于默认值解析一段配置 JSON（不读文件、不读环境变量、**不做迁移**）。
//
// 版本闸门：只接受当前版本（CurrentVersion）或完全不带 config_version 的 JSON。
// 显式声明旧版本的 JSON 一律报错，要求走 Load / MigrateMap 的迁移路径——
// 这是"不再长期归一化"的执行点：旧形态不能被直接解析，也就不会有兼容分支
// 悄悄长在 normalize 里。
func ParseConfig(raw []byte) (*Config, error) {
	return ParseConfigInto(raw, Default())
}

// ParseConfigInto 把 JSON 覆盖到 c 上并 normalize（不读文件、不读 env、不做迁移）。
//
// 调用方若是从磁盘/面板提交里拿到可能过期的 JSON，应先过 MigrateMap（或直接 Load）：
// 本函数只认当前版本。
func ParseConfigInto(raw []byte, c *Config) (*Config, error) {
	obj, err := jsondoc.Object(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return parseObject(obj, c)
}

// WriteDefault 在 path 落一份推荐配置（首次运行自动生成，双击即开免手工复制样例）。
// 值取自 Default()（含超时/熔断/签到排程等推荐值），api_key 用 crypto/rand 随机生成：
// 安全默认优于示例占位符（listen 绑定 0.0.0.0，空 key 会把网关裸暴露给局域网）。
// 返回生成的 key 供启动日志透出。已存在时经 O_EXCL 原子拒绝，绝不改写用户配置。
func WriteDefault(path string) (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("gen api_key: %w", err)
	}
	key := "sk-" + base64.RawURLEncoding.EncodeToString(raw)
	c := Default()
	c.APIKey = key
	_ = c.normalize() // Default() 全合法，normalize 仅补齐 header/idle 超时的展示值
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir config dir: %w", err)
		}
	}
	// O_EXCL 原子拒绝覆盖：即使调用方漏判"不存在"，也绝不悄悄改写用户已有配置。
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(out); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return key, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("WB2A_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("WB2A_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("WB2A_AUTH_DIR"); v != "" {
		c.AuthDir = v
	}
	if v := os.Getenv("WB2A_STATE_FILE"); v != "" {
		c.StateFile = v
	}
	if v := os.Getenv("WB2A_SOFT_RATE"); v != "" {
		c.Cooldown.SoftRate = v
	}
	if v := os.Getenv("WB2A_SOFT_RATE_MAX"); v != "" {
		c.Cooldown.SoftRateMax = v
	}
	if v := os.Getenv("WB2A_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Upstream.TimeoutSeconds = n
		}
	}
	if v := os.Getenv("WB2A_HEADER_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Upstream.HeaderTimeoutSeconds = n
		}
	}
	if v := os.Getenv("WB2A_IDLE_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Upstream.IdleTimeoutSeconds = n
		}
	}

	if v := os.Getenv("WB2A_FIRST_MODEL_EVENT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Upstream.FirstModelEventSeconds = n
		}
	}
	if v := os.Getenv("WB2A_FIRST_GENERATION_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Upstream.FirstGenerationSeconds = n
		}
	}
	if v := os.Getenv("WB2A_TAIL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Upstream.TailSeconds = n
		}
	}
	if v := os.Getenv("WB2A_CLIENT_NAME"); v != "" {
		c.Upstream.ClientName = v
	}
	if v := os.Getenv("WB2A_DEVICE_TOKEN"); v != "" {
		c.Upstream.DeviceToken = v
	}
	if v := os.Getenv("WB2A_DEVICE_TOKEN_FILE"); v != "" {
		c.Upstream.DeviceTokenFile = v
	}
	if v := os.Getenv("WB2A_PASSTHROUGH_IP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Upstream.PassthroughIP = b
		}
	}

	if v := os.Getenv("WB2A_FINGERPRINT_REWRITE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.FingerprintRewrite = b
		}
	}
	if v := os.Getenv("WB2A_PROMPT_MODE"); v != "" {
		c.Prompt.Mode = v
	}
	if v := os.Getenv("WB2A_PROMPT_FILE"); v != "" {
		c.Prompt.File = v
	}
	if v := os.Getenv("WB2A_EXPIRING_SOON"); v != "" {
		c.Pool.ExpiringSoon = v
	}
	if v := os.Getenv("WB2A_PREFER_EXPIRING"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Pool.PreferExpiring = b
		}
	}
	if v := os.Getenv("WB2A_PROXY_URL"); v != "" {
		c.ProxyURL = v
	}
	if v := os.Getenv("WB2A_RESIN_URL"); v != "" {
		c.ResinURL = v
	}
	if v := os.Getenv("WB2A_RESIN_PLATFORM_NAME"); v != "" {
		c.ResinPlatformName = v
	}
	if v := os.Getenv("WB2A_RESIN_MODE"); v != "" {
		c.ResinMode = v
	}
	if v := os.Getenv("WB2A_RESIN_AUTH_VERSION"); v != "" {
		c.ResinAuthVersion = v
	}
	if v := os.Getenv("WB2A_MODEL_DEFAULT_REALM"); v != "" {
		c.ModelDefaultRealm = v
	}
}
