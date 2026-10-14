package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeDataDoesNotRestoreDeletedItems(t *testing.T) {
	tempDir := t.TempDir()
	databasePath := filepath.Join(tempDir, "devstash.db")
	legacyPath := filepath.Join(tempDir, "items.json")

	legacyItems := []StashItem{
		{ID: 1, Title: "旧资料", Content: "legacy", Tags: []string{"test"}},
	}
	legacyJSON, err := json.Marshal(legacyItems)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyJSON, 0644); err != nil {
		t.Fatal(err)
	}

	testDB, err := openDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()

	if err := initializeData(testDB, legacyPath); err != nil {
		t.Fatal(err)
	}
	if err := deleteItemFromDB(context.Background(), testDB, 1); err != nil {
		t.Fatal(err)
	}

	// 模拟服务重启时再次执行初始化。
	if err := initializeData(testDB, legacyPath); err != nil {
		t.Fatal(err)
	}

	items, err := queryItems(context.Background(), testDB, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("删除全部资料后重新初始化，期望 0 条，实际得到 %d 条", len(items))
	}
}
