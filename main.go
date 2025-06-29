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

	// 匿名导入以支持解码 gif 和 png 图片
	_ "image/gif"
	_ "image/png"

	"github.com/google/uuid"
	"golang.org/x/image/webp"
)

// indexHTML 使用 embed.FS 嵌入 index.html 文件，以便在编译后的二进制文件中直接提供前端页面。
//
//go:embed index.html
var indexHTML embed.FS

// uploadDir 定义了用于存储上传文件的目录名称。
const uploadDir = "updata"

// 密码保护配置
const (
	// 默认密码（您可以修改这个密码）
	defaultPassword = "notepad123"

	// 是否启用密码保护（设为false可完全禁用密码保护）
	enablePasswordProtection = true
)

// 密码验证函数
func verifyPassword(inputPassword string) bool {
	// 如果禁用了密码保护，总是返回true
	if !enablePasswordProtection {
		return true
	}
	// 这里使用简单的字符串比较，您可以根据需要改为更安全的哈希验证
	return inputPassword == defaultPassword
}

// main 函数是程序的入口点。
func main() {
	// 确保上传目录存在，如果不存在则创建它。
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.Mkdir(uploadDir, 0755)
	}

	// 注册根路径 ("/") 的请求处理器。
	http.HandleFunc("/", handleRequest)
	// 为上传的文件提供服务。"/updata/" URL 路径下的请求会映射到 "updata" 目录。
	http.Handle("/updata/", http.StripPrefix("/updata/", http.FileServer(http.Dir(uploadDir))))

	// 启动 HTTP 服务器并监听 8080 端口。
	fmt.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// handleRequest 是所有进入请求的总处理器。它根据请求方法和内容进行分发。
func handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// 处理 POST 请求
		if r.FormValue("content") != "" {
			// 如果请求包含 "content" 表单值，则为保存笔记。
			saveNoteHandler(w, r)
		} else if r.FormValue("image") != "" {
			// 如果请求包含 "image" 表单值，则为粘贴图片。
			pasteImageHandler(w, r)
		} else {
			// 否则，处理文件上传。
			uploadFileHandler(w, r)
		}
	} else if r.Method == http.MethodGet {
		// 处理 GET 请求
		// 检查是否为 AJAX 请求，用于加载笔记。
		if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			loadNoteHandler(w, r)
		} else {
			// 否则，提供主页 HTML。
			fs, err := indexHTML.ReadFile("index.html")
			if err != nil {
				http.Error(w, "Could not read embedded file", http.StatusInternalServerError)
				return
			}
			w.Write(fs)
		}
	} else {
		// 对于不支持的请求方法，返回错误。
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// getSafeFileName 根据给定的 ID 生成一个安全的文件名。
// 它会移除所有非字母和数字的字符，并添加 ".txt" 后缀。
func getSafeFileName(id string) string {
	reg, _ := regexp.Compile("[^a-zA-Z0-9]+")
	return reg.ReplaceAllString(id, "") + ".txt"
}

// saveNoteHandler 处理保存笔记内容的请求。
func saveNoteHandler(w http.ResponseWriter, r *http.Request) {
	noteID := r.FormValue("noteId")
	content := r.FormValue("content")
	password := r.FormValue("password")

	// 只有在启用密码保护时才验证密码
	if enablePasswordProtection {
		if !verifyPassword(password) {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Invalid password"}, http.StatusUnauthorized)
			return
		}
	}

	filename := filepath.Join(uploadDir, getSafeFileName(noteID))

	// 将内容写入文件。
	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to save note"}, http.StatusInternalServerError)
		return
	}
	// 返回成功响应。
	jsonResponse(w, map[string]interface{}{"success": true}, http.StatusOK)
}

// loadNoteHandler 处理加载笔记内容的请求。
func loadNoteHandler(w http.ResponseWriter, r *http.Request) {
	noteID := r.URL.Query().Get("note")
	filename := filepath.Join(uploadDir, getSafeFileName(noteID))

	// 检查文件是否存在。
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		// 如果文件不存在，返回空内容。
		w.Write([]byte(""))
		return
	}

	// 读取并返回文件内容。
	content, err := os.ReadFile(filename)
	if err != nil {
		http.Error(w, "Failed to read note", http.StatusInternalServerError)
		return
	}
	w.Write(content)
}

// pasteImageHandler 处理通过粘贴方式上传的 Base64 编码的图片。
func pasteImageHandler(w http.ResponseWriter, r *http.Request) {
	data := r.FormValue("image")
	// Base64 数据通常以 "data:image/png;base64," 开头，这里分割获取数据部分。
	parts := strings.Split(data, ",")
	if len(parts) != 2 {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Invalid image data"}, http.StatusBadRequest)
		return
	}

	// 解码 Base64 字符串。
	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to decode image"}, http.StatusBadRequest)
		return
	}

	// 将字节数据解码为 image.Image 对象。
	img, _, err := image.Decode(strings.NewReader(string(imageData)))
	if err != nil {
		// 如果标准库解码失败，尝试作为 webp 格式解码。
		img, err = webp.Decode(strings.NewReader(string(imageData)))
		if err != nil {
			jsonResponse(w, map[string]interface{}{"success": false, "error": "Unsupported image format"}, http.StatusBadRequest)
			return
		}
	}

	// 生成一个唯一的文件名，并保存为 JPG 格式。
	filename := filepath.Join(uploadDir, uuid.New().String()+".jpg")
	outFile, err := os.Create(filename)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to create image file"}, http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

	// 将图片编码为 JPG 并保存。
	err = jpeg.Encode(outFile, img, &jpeg.Options{Quality: 80})
	if err != nil {
		jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to convert image to JPG"}, http.StatusInternalServerError)
		return
	}

	// 返回包含新图片 URL 的成功响应。
	jsonResponse(w, map[string]interface{}{"success": true, "url": strings.ReplaceAll(filename, "\\", "/")}, http.StatusOK)
}

// uploadFileHandler 处理通过文件上传表单上传的文件。
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
	// 检查文件扩展名是否为图片。
	for _, e := range imageExts {
		if ext == e {
			isImage = true
			break
		}
	}

	if isImage {
		// 处理图片文件。
		img, _, err := image.Decode(file)
		if err != nil {
			// 如果标准库解码失败，重置文件指针并尝试作为 webp 解码。
			file.Seek(0, io.SeekStart)
			img, err = webp.Decode(file)
			if err != nil {
				jsonResponse(w, map[string]interface{}{"success": false, "error": "Failed to decode image"}, http.StatusBadRequest)
				return
			}
		}

		// 将图片转换为 JPG 格式并保存。
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
		jsonResponse(w, map[string]interface{}{"success": true, "url": strings.ReplaceAll(filename, "\\", "/"), "filename": strings.TrimSuffix(handler.Filename, ext) + ".jpg"}, http.StatusOK)

	} else {
		// 处理其他类型的文件。
		// 限制文件大小不能超过 20MB。
		if handler.Size > 20*1024*1024 { // 20MB
			jsonResponse(w, map[string]interface{}{"success": false, "error": "File size cannot exceed 20MB"}, http.StatusBadRequest)
			return
		}

		// 检查文件类型是否在允许的列表中。
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

		// 保存原始文件。
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
		jsonResponse(w, map[string]interface{}{"success": true, "url": strings.ReplaceAll(filename, "\\", "/"), "filename": handler.Filename}, http.StatusOK)
	}
}

// jsonResponse 是一个辅助函数，用于构建并发送 JSON 格式的 HTTP 响应。
func jsonResponse(w http.ResponseWriter, data map[string]interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
