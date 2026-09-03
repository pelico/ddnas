// Package config 采集与持久化中间件配置。
// 配置以 YAML 形式存放在卷映射目录（默认 /data/config.yaml），
// 供 Admin Web UI 读写，重启容器后配置保留。
package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrNotFound 表示配置文件尚未创建（首次启动）。
var ErrNotFound = errors.New("config file not found")

// Config 中间件完整配置。
type Config struct {
	Server   ServerCfg                  `yaml:"server"`
	Auth     AuthCfg                   `yaml:"auth"`
	Adapters map[string]map[string]any  `yaml:"adapters"`
}

// ServerCfg 启动级配置（一般不由 UI 改，由环境/默认值决定）。
type ServerCfg struct {
	Addr    string `yaml:"addr"`
	DataDir string `yaml:"data_dir"`
}

// AuthCfg 鉴权配置。AdminPass 存放 bcrypt 哈希；AppToken 为 App 访问令牌明文。
type AuthCfg struct {
	AdminUser string `yaml:"admin_user"`
	AdminPass string `yaml:"admin_pass"`
	AppToken  string `yaml:"app_token"`
}

// Store 线程安全的配置读写。
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// New 创建 Store。path 为配置文件完整路径。
func New(path string) *Store {
	return &Store{path: path}
}

// Path 返回配置文件路径。
func (s *Store) Path() string { return s.path }

// Load 从磁盘加载配置。文件不存在时返回 ErrNotFound。
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Adapters == nil {
		c.Adapters = map[string]map[string]any{}
	}
	s.cfg = c
	return nil
}

// Save 写入配置到磁盘（原子替换）。
func (s *Store) Save(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c.Adapters == nil {
		c.Adapters = map[string]map[string]any{}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.cfg = c
	return nil
}

// Get 返回当前配置的副本。
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 浅拷贝足够：字段都是值类型或会被调用方只读使用。
	// adapters map 调用方不应原地修改。
	return s.cfg
}

// AdapterConfig 返回某 adapter 的配置段，没有则返回空 map。
func (s *Store) AdapterConfig(name string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg.Adapters == nil {
		return map[string]any{}
	}
	if m, ok := s.cfg.Adapters[name]; ok && m != nil {
		return m
	}
	return map[string]any{}
}

// Configured 判断是否已完成首次配置（有管理员密码即视为已配置）。
func (s *Store) Configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Auth.AdminUser != "" && s.cfg.Auth.AdminPass != ""
}
