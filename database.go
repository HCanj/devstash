package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const legacyMigrationKey = "legacy_json_migrated"

func openDatabase(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// DevStash 是单机小工具，限制为一个连接可避免多个写操作争抢 SQLite 文件。
	database.SetMaxOpenConns(1)

	if err := database.Ping(); err != nil {
		database.Close()
		return nil, err
	}

	const itemsSchema = `
		CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]'
		)
	`
	const metadataSchema = `
		CREATE TABLE IF NOT EXISTS app_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`
	for _, schema := range []string{itemsSchema, metadataSchema} {
		if _, err := database.Exec(schema); err != nil {
			database.Close()
			return nil, err
		}
	}

	return database, nil
}

// initializeData 只迁移一次旧 JSON 数据。迁移完成后写入 app_meta 标记，
// 即使用户后来删除全部资料，也不会重新导入旧数据。
func initializeData(database *sql.DB, legacyPath string) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var migrationValue string
	err = tx.QueryRow(
		"SELECT value FROM app_meta WHERE key = ?",
		legacyMigrationKey,
	).Scan(&migrationValue)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// 兼容已经运行过上一版 SQLite 代码的数据库：如果 items 中已有数据，
	// 只补写迁移标记，不再重复导入 JSON。
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		return err
	}

	if count == 0 {
		legacyItems, err := readLegacyItems(legacyPath)
		if errors.Is(err, os.ErrNotExist) {
			legacyItems = []StashItem{
				{ID: 1, Title: "查看go版本", Content: "go version", Tags: []string{"go", "命令"}},
			}
		} else if err != nil {
			return err
		}

		for _, item := range legacyItems {
			tagsJSON, err := encodeTags(item.Tags)
			if err != nil {
				return err
			}

			_, err = tx.Exec(
				"INSERT INTO items (id, title, content, tags) VALUES (?, ?, ?, ?)",
				item.ID,
				item.Title,
				item.Content,
				tagsJSON,
			)
			if err != nil {
				return err
			}
		}
	}

	_, err = tx.Exec(
		"INSERT INTO app_meta (key, value) VALUES (?, ?)",
		legacyMigrationKey,
		"true",
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func readLegacyItems(path string) ([]StashItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var items []StashItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("解析旧 JSON 数据失败: %w", err)
	}
	return items, nil
}

func queryItems(ctx context.Context, database *sql.DB, keyword string) ([]StashItem, error) {
	keyword = strings.TrimSpace(keyword)
	query := "SELECT id, title, content, tags FROM items ORDER BY id"
	var args []any

	if keyword != "" {
		query = `
			SELECT id, title, content, tags
			FROM items
			WHERE LOWER(title) LIKE LOWER(?)
			   OR LOWER(content) LIKE LOWER(?)
			   OR LOWER(tags) LIKE LOWER(?)
			ORDER BY id
		`
		pattern := "%" + keyword + "%"
		args = []any{pattern, pattern, pattern}
	}

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []StashItem{}
	for rows.Next() {
		var item StashItem
		var tagsJSON string
		if err := rows.Scan(&item.ID, &item.Title, &item.Content, &tagsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &item.Tags); err != nil {
			return nil, fmt.Errorf("解析资料 %d 的标签失败: %w", item.ID, err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func insertItem(ctx context.Context, database *sql.DB, req CreateItemRequest) (StashItem, error) {
	tagsJSON, err := encodeTags(req.Tags)
	if err != nil {
		return StashItem{}, err
	}

	result, err := database.ExecContext(
		ctx,
		"INSERT INTO items (title, content, tags) VALUES (?, ?, ?)",
		req.Title,
		req.Content,
		tagsJSON,
	)
	if err != nil {
		return StashItem{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return StashItem{}, err
	}

	return StashItem{
		ID:      int(id),
		Title:   req.Title,
		Content: req.Content,
		Tags:    normalizedTags(req.Tags),
	}, nil
}

func updateItemInDB(ctx context.Context, database *sql.DB, id int, req UpdateItemRequest) (StashItem, error) {
	tagsJSON, err := encodeTags(req.Tags)
	if err != nil {
		return StashItem{}, err
	}

	result, err := database.ExecContext(
		ctx,
		"UPDATE items SET title = ?, content = ?, tags = ? WHERE id = ?",
		req.Title,
		req.Content,
		tagsJSON,
		id,
	)
	if err != nil {
		return StashItem{}, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return StashItem{}, err
	}
	if affected == 0 {
		return StashItem{}, sql.ErrNoRows
	}

	return StashItem{
		ID:      id,
		Title:   req.Title,
		Content: req.Content,
		Tags:    normalizedTags(req.Tags),
	}, nil
}

func deleteItemFromDB(ctx context.Context, database *sql.DB, id int) error {
	result, err := database.ExecContext(ctx, "DELETE FROM items WHERE id = ?", id)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func encodeTags(tags []string) (string, error) {
	data, err := json.Marshal(normalizedTags(tags))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizedTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
