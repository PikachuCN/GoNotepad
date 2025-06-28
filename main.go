package main

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "image/gif"
	_ "image/png"

	"github.com/google/uuid"
	"golang.org/x/image/webp"
)

//go:embed index.html
var indexHTML embed.FS

const uploadDir = "updata"

func main() {
	// 确保上传目录存在
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.Mkdir(uploadDir, 0755)
	}

	http.HandleFunc("/", handleRequest)
	http.Handle("/updata/", http.StripPrefix("/updata/", http.FileServer(http.Dir(uploadDir))))

	fmt.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// 根据不同的 POST 请求进行分发
		if r.FormValue("content") != "" {
			saveNoteHandler(w, r)
		} else if r.FormValue("image") != "" {
			pasteImageHandler(w, r)
		} else {
			uploadFileHandler(w, r)
		}
	} else if r.Method == http.MethodGet {
		// 检查是否为 AJAX 请求
		if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			loadNoteHandler(w, r)
		} else {
			// 提供主页
			fs, err := indexHTML.ReadFile("index.html")
			if err != nil {
				http.Error(w, "Could not read embedded file", http.StatusInternalServerError)
				return
			}
			w.Write(fs)
		}
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func getSafeFileName(id string) string {
	reg, _ := regexp.Compile("[^a-zA-Z0-9]+")
	return reg.ReplaceAllString(id, "") + ".txt"
}

func saveNoteHandler(w http.ResponseWriter, r *http.Request) {
	noteID := r.FormValue("noteId")
	content := r.FormValue("content")
	filename := filepath.Join(uploadDir, getSafeFileName(noteID))

	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to save note"}, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]interface{}{"success": true}, http.StatusOK)
}

func loadNoteHandler(w http.ResponseWriter, r *http.Request) {
	noteID := r.URL.Query().Get("note")
	filename := filepath.Join(uploadDir, getSafeFileName(noteID))

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		w.Write([]byte(""))
		return
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		http.Error(w, "Failed to read note", http.StatusInternalServerError)
		return
	}
	w.Write(content)
}

func pasteImageHandler(w http.ResponseWriter, r *http.Request) {
	data := r.FormValue("image")
	parts := strings.Split(data, ",")
	if len(parts) != 2 {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Invalid image data"}, http.StatusBadRequest)
		return
	}

	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to decode image"}, http.StatusBadRequest)
		return
	}

	img, _, err := image.Decode(strings.NewReader(string(imageData)))
	if err != nil {
		// 尝试解码 webp
		img, err = webp.Decode(strings.NewReader(string(imageData)))
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Unsupported image format"}, http.StatusBadRequest)
			return
		}
	}

	filename := filepath.Join(uploadDir, uuid.New().String()+".jpg")
	outFile, err := os.Create(filename)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to create image file"}, http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

	err = jpeg.Encode(outFile, img, &jpeg.Options{Quality: 80})
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to convert image to JPG"}, http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{"success": true, "url": filename}, http.StatusOK)
}

func uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	file, handler, err := r.FormFile("file")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to get uploaded file"}, http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(handler.Filename))
	isImage := false
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	for _, e := range imageExts {
		if ext == e {
			isImage = true
			break
		}
	}

	if isImage {
		img, _, err := image.Decode(file)
		if err != nil {
			// 如果标准库解码失败，尝试webp
			file.Seek(0, io.SeekStart) // 重置文件指针
			img, err = webp.Decode(file)
			if err != nil {
				jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to decode image"}, http.StatusBadRequest)
				return
			}
		}

		filename := filepath.Join(uploadDir, uuid.New().String()+".jpg")
		outFile, err := os.Create(filename)
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to create image file"}, http.StatusInternalServerError)
			return
		}
		defer outFile.Close()

		err = jpeg.Encode(outFile, img, &jpeg.Options{Quality: 80})
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to convert image to JPG"}, http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{"success": true, "url": filename, "filename": strings.TrimSuffix(handler.Filename, ext) + ".jpg"}, http.StatusOK)

	} else {
		// 处理其他文件
		if handler.Size > 20*1024*1024 { // 20MB
			jsonResponse(w, map[string]interface{}{"success": false, "error": "File size cannot exceed 20MB"}, http.StatusBadRequest)
			return
		}

		allowedExts := []string{".txt", ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".zip", ".rar", ".7z"}
		allowed := false
		for _, e := range allowedExts {
			if ext == e {
				allowed = true
				break
			}
		}
		if !allowed {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "File type not allowed"}, http.StatusBadRequest)
			return
		}

		filename := filepath.Join(uploadDir, uuid.New().String()+ext)
		outFile, err := os.Create(filename)
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to create file"}, http.StatusInternalServerError)
			return
		}
		defer outFile.Close()

		_, err = io.Copy(outFile, file)
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to save file"}, http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{"success": true, "url": filename, "filename": handler.Filename}, http.StatusOK)
	}
}

func jsonResponse(w http.ResponseWriter, data map[string]interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
