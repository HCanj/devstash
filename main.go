package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
)

var db *sql.DB

type HealthResponse struct {
	Status string `json:"status"`
}

type StashItem struct {
	ID      int      `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type CreateItemRequest struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type UpdateItemRequest struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if err := db.PingContext(r.Context()); err != nil {
		log.Println("数据库健康检查失败：", err)
		http.Error(w, "服务暂不可用", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
	if err != nil {
		log.Println("写入健康检查响应失败：", err)
	}
}

func listItemsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keyword := r.URL.Query().Get("keyword")
		items, err := queryItems(r.Context(), db, keyword)
		if err != nil {
			log.Println("查询资料失败：", err)
			http.Error(w, "查询资料失败", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(items); err != nil {
			log.Println("写入资料列表失败：", err)
		}

	case http.MethodPost:
		var req CreateItemRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "参数格式有误", http.StatusBadRequest)
			return
		}

		req.Title = strings.TrimSpace(req.Title)
		req.Content = strings.TrimSpace(req.Content)
		if req.Title == "" || req.Content == "" {
			http.Error(w, "标题和内容不能为空。", http.StatusBadRequest)
			return
		}

		item, err := insertItem(r.Context(), db, req)
		if err != nil {
			log.Println("创建资料失败：", err)
			http.Error(w, "创建资料失败", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(item); err != nil {
			log.Println("写入创建响应失败：", err)
		}

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "只支持 GET、POST", http.StatusMethodNotAllowed)
	}
}

func itemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		w.Header().Set("Allow", "DELETE, PUT")
		http.Error(w, "只支持 DELETE、PUT", http.StatusMethodNotAllowed)
		return
	}

	rawID := strings.TrimPrefix(r.URL.Path, "/api/items/")
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		http.Error(w, "资料 ID 有误", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		deleteItem(w, r, id)
	case http.MethodPut:
		updateItem(w, r, id)
	}
}

func deleteItem(w http.ResponseWriter, r *http.Request, id int) {
	err := deleteItemFromDB(r.Context(), db, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "资料不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Println("删除资料失败：", err)
		http.Error(w, "删除资料失败", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func updateItem(w http.ResponseWriter, r *http.Request, id int) {
	var req UpdateItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "参数格式有误", http.StatusBadRequest)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	if req.Title == "" || req.Content == "" {
		http.Error(w, "标题和内容不能为空。", http.StatusBadRequest)
		return
	}

	item, err := updateItemInDB(r.Context(), db, id, req)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "资料不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Println("修改资料失败：", err)
		http.Error(w, "修改资料失败", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(item); err != nil {
		log.Println("写入修改响应失败：", err)
	}
}

func main() {
	database, err := openDatabase("data/devstash.db")
	if err != nil {
		log.Fatal("数据库初始化失败：", err)
	}
	db = database
	defer db.Close()

	if err := initializeData(db, "data/items.json"); err != nil {
		log.Fatal("初始化资料失败：", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/items", listItemsHandler)
	mux.HandleFunc("/api/items/", itemHandler)

	fileServer := http.FileServer(http.Dir("./web"))
	mux.Handle("/", fileServer)

	log.Println("DevStash 正在监听 http://127.0.0.1:8090")
	log.Fatal(http.ListenAndServe("127.0.0.1:8090", mux))
}
