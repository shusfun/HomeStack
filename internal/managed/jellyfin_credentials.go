package managed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func recoverJellyfinAPIKey(ctx context.Context, stateDir string) (string, error) {
	databasePath := filepath.Join(stateDir, "jellyfin", "data", "data", "jellyfin.db")
	info, err := os.Stat(databasePath)
	if err != nil {
		return "", fmt.Errorf("读取 Jellyfin API Key 数据库失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Jellyfin API Key 数据库不是常规文件")
	}

	dsn, err := sqliteReadOnlyDSN(databasePath)
	if err != nil {
		return "", err
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("打开 Jellyfin API Key 数据库失败: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()

	rows, err := database.QueryContext(ctx, `SELECT "AccessToken" FROM "ApiKeys" WHERE "Name" = ?`, "HomeStack")
	if err != nil {
		return "", fmt.Errorf("查询 Jellyfin HomeStack API Key 失败: %w", err)
	}
	defer rows.Close()

	apiKey := ""
	count := 0
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", fmt.Errorf("读取 Jellyfin HomeStack API Key 失败: %w", err)
		}
		count++
		if count > 1 {
			return "", errors.New("Jellyfin 数据库存在多个 HomeStack API Key，无法确定迁移凭据")
		}
		apiKey = candidate
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("遍历 Jellyfin HomeStack API Key 失败: %w", err)
	}
	if count == 0 {
		return "", errors.New("Jellyfin 数据库中未找到 HomeStack API Key")
	}
	if apiKey == "" {
		return "", errors.New("Jellyfin 数据库中的 HomeStack API Key 为空")
	}
	return apiKey, nil
}

func sqliteReadOnlyDSN(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("Jellyfin API Key 数据库路径必须是绝对路径")
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String(), nil
}
