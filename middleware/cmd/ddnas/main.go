// DDNAS 中间件入口。
// 默认数据目录 /data（卷映射），配置文件 /data/config.yaml。
// 首次启动无配置时，Admin 控制台引导完成设置；之后保存即热重载。
package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/pelico/ddnas/middleware/internal/config"
	"github.com/pelico/ddnas/middleware/internal/server"
	"github.com/pelico/ddnas/middleware/internal/plugin"
	"github.com/pelico/ddnas/middleware/internal/store"
	_ "github.com/pelico/ddnas/middleware/plugins/downloader"
	_ "github.com/pelico/ddnas/middleware/plugins/nodeexporter"
	_ "github.com/pelico/ddnas/middleware/plugins/openlist"
)

func main() {
	dataDir := envOr("DDNAS_DATA_DIR", "/data")
	addr := envOr("DDNAS_ADDR", ":8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录 %s 失败: %v", dataDir, err)
	}
	cfgPath := filepath.Join(dataDir, "config.yaml")
	cfgStore := config.New(cfgPath)
	if err := cfgStore.Load(); err != nil && err != config.ErrNotFound {
		log.Printf("加载配置失败(将使用空配置): %v", err)
	}

	// SQLite 持久层：备份历史、下载任务等运行时数据
	db, err := store.New(dataDir)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer db.Close()

	adapters := plugin.Build()
	srv := server.New(cfgStore, adapters, db)

	log.Printf("DDNAS 中间件启动于 %s，数据目录 %s", addr, dataDir)
	if !cfgStore.Configured() {
		log.Printf("首次启动：请访问 http://<宿主>%s/ 完成设置向导", addr)
	}

	go func() {
		if err := srv.Run(addr); err != nil {
			log.Fatalf("服务退出: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("正在关闭...")
	_ = srv.Shutdown(nil)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
