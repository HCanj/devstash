package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

var itemsMu sync.Mutex

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

var items = []StashItem{
	{
		ID:      1,
		Title:   "查看go版本",
		Content: "go version",
		Tags:    []string{"go", "命令"},
	},
}

var nextID = 2

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(w).Encode(HealthResponse{
		Status: "ok",
	})

	if err != nil {
		log.Println("写入健康检查响应失败：", err)
	}

}

func listItemsHandler(w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:

		var snapshot []StashItem

		itemsMu.Lock()
		snapshot = make([]StashItem, len(items))
		copy(snapshot, items)

		itemsMu.Unlock()

		//根据关键词获取过滤后信息
		keyword := r.URL.Query().Get("keyword")
		filtered := filterItems(snapshot, keyword)

		w.Header().Set("Content-type", "application/json")

		//如果关键词为空，相当于返回完整项目，否则正常返回
		err := json.NewEncoder(w).Encode(filtered)

		if err != nil {
			log.Println("写入资料列表失败：", err)
		}
	case http.MethodPost:
		var req CreateItemRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, "参数格式有误", http.StatusBadRequest)
			return
		}

		req.Title = strings.TrimSpace(req.Title)
		req.Content = strings.TrimSpace(req.Content)

		if req.Title == "" || req.Content == "" {
			http.Error(w, "标题和内容不能为空。", http.StatusBadRequest)
			return
		}

		itemsMu.Lock()
		defer itemsMu.Unlock()

		stashitem := StashItem{
			ID:      nextID,
			Title:   req.Title,
			Content: req.Content,
			Tags:    req.Tags,
		}

		newItems := append(items, stashitem)
		err = saveItems(newItems)
		if err != nil {
			fmt.Println("文件存储有误：", err)
			http.Error(w, "文件存储有误", http.StatusInternalServerError)
			return
		}
		items = newItems
		nextID++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		err = json.NewEncoder(w).Encode(stashitem)
		if err != nil {
			fmt.Println("写入响应失败： ", err)
		}

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "只支持GET，POST", http.StatusMethodNotAllowed)
	}

}

func itemHandler(w http.ResponseWriter, r *http.Request) {
	//检查接口方式
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		w.Header().Set("Allow", "DELETE, PUT")
		http.Error(w, "只支持 DELETE、PUT", http.StatusMethodNotAllowed)
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/api/items/")

	//格式转换
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		http.Error(w, "资料ID有误，", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		deleteItem(w, id)
	case http.MethodPut:
		updateItem(w, r, id)
	}
}

func deleteItem(w http.ResponseWriter, id int) {
	itemsMu.Lock()
	defer itemsMu.Unlock()

	newItems := make([]StashItem, 0, len(items))
	found := false

	for _, item := range items {
		if item.ID == id {
			found = true
			//continue 跳过本次循环，这里是把其他的都加到新的数组中去
			continue
		}

		newItems = append(newItems, item)

	}
	if !found {
		http.Error(w, "资料不存在", http.StatusNotFound)
		return
	}
	err := saveItems(newItems)
	if err != nil {
		log.Println("保存删除结果失败：", err)
		http.Error(w, "删除资料失败", http.StatusInternalServerError)
		return
	}
	items = newItems
	w.WriteHeader(http.StatusNoContent)
}

func updateItem(w http.ResponseWriter, r *http.Request, id int) {
	var req UpdateItemRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "参数格式有误", http.StatusBadRequest)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	if req.Title == "" || req.Content == "" {
		http.Error(w, "标题和内容不能为空。", http.StatusBadRequest)
		return
	}

	itemsMu.Lock()
	defer itemsMu.Unlock()

	newItems := make([]StashItem, len(items))
	copy(newItems, items)

	found := false
	var updatedItem StashItem
	for i, item := range newItems {
		if item.ID == id {
			updatedItem = StashItem{
				ID:      id,
				Title:   req.Title,
				Content: req.Content,
				Tags:    req.Tags,
			}
			newItems[i] = updatedItem
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "资料不存在", http.StatusNotFound)
		return
	}

	err = saveItems(newItems)
	if err != nil {
		log.Println("保存修改结果失败：", err)
		http.Error(w, "修改资料失败", http.StatusInternalServerError)
		return
	}

	items = newItems
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(updatedItem)
	if err != nil {
		log.Println("写入修改响应失败：", err)
	}

}

func loadItems() ([]StashItem, error) {
	var items []StashItem
	jsonData, err := os.ReadFile("data/items.json")
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(jsonData, &items)
	if err != nil {
		return nil, err
	}
	return items, nil

}

func saveItems(items []StashItem) error {
	jsonData, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	err = os.MkdirAll("data", 0755)
	if err != nil {
		return err
	}
	err = os.WriteFile("data/items.json", jsonData, 0644)
	if err != nil {
		return err
	}
	return nil

}

func findNextID(items []StashItem) int {
	maxID := 0
	for _, item := range items {
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	return maxID + 1
}

// 根据关键词过滤信息
func filterItems(items []StashItem, keyword string) []StashItem {
	keyword = strings.ToLower(strings.TrimSpace(keyword))

	if keyword == "" {
		return items
	}

	filtered := []StashItem{}

	for _, item := range items {
		title := strings.ToLower(item.Title)
		content := strings.ToLower(item.Content)

		matched := strings.Contains(title, keyword) ||
			strings.Contains(content, keyword)

		if !matched {
			for _, tag := range item.Tags {
				tag = strings.ToLower(tag)
				if strings.Contains(tag, keyword) {
					matched = true
					break
				}
			}
		}
		if matched {
			filtered = append(filtered, item)
		}

	}
	return filtered
}

func main() {

	loadedItems, err := loadItems()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Println("资料尚不存在，使用默认资料")
		} else {
			log.Fatal("文件读取有误：", err)
		}
	} else {
		items = loadedItems
		nextID = findNextID(items)
	}

	err = saveItems(items)
	if err != nil {
		log.Fatal("文件存储有误：", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/items", listItemsHandler)
	mux.HandleFunc("/api/items/", itemHandler)

	fileServer := http.FileServer(http.Dir("./web"))
	mux.Handle("/", fileServer)

	log.Fatal(http.ListenAndServe("127.0.0.1:8090", mux))
}
