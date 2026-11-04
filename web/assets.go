// Package web 内嵌前端资源：模板、样式、脚本与 mermaid。
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"strings"
	"sync"
)

// assetNames 是允许通过 /-/vendor/ 访问的全部文件。
// 用白名单而不是遍历目录：既杜绝路径穿越，也不会因为新增文件而意外暴露。
var assetNames = []string{
	"app.css",
	"app.js",
	"mermaid.js",
	"mermaid-lazy.js",
	"mermaid-frame.js",
	"mermaid.min.js",
	"mono-cjk.woff2",
	"mono-latin.woff2",
}

var assetTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".woff2": "font/woff2",
}

// Asset 是一份内嵌资源。压缩结果按需生成并缓存。
type Asset struct {
	Name string
	Data []byte
	// ETag 是内容哈希的前 8 字节，内容变则变。
	ETag string
	Type string

	once sync.Once
	gz   []byte
}

// Gzip 返回 gzip 压缩后的内容；首次调用时压缩，之后走缓存。
func (a *Asset) Gzip() []byte {
	a.once.Do(func() {
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return
		}
		if _, err := zw.Write(a.Data); err != nil {
			return
		}
		if err := zw.Close(); err != nil {
			return
		}
		a.gz = buf.Bytes()
	})
	return a.gz
}

var (
	assetsOnce sync.Once
	assets     map[string]*Asset
	version    string
)

func loadAssets() {
	assetsOnce.Do(func() {
		assets = make(map[string]*Asset, len(assetNames))
		fingerprint := make([]byte, 0, len(assetNames)*32)
		for _, name := range assetNames {
			b, err := fs.ReadFile(FS, "vendor/"+name)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(b)
			etag := hex.EncodeToString(sum[:8])
			assets[name] = &Asset{Name: name, Data: b, ETag: etag, Type: assetType(name)}
			fingerprint = append(fingerprint, name...)
			fingerprint = append(fingerprint, 0)
			fingerprint = append(fingerprint, etag...)
			fingerprint = append(fingerprint, 0)
		}
		// 整体版本：任一资源变化都会让 ?v= 变化，浏览器与边缘缓存自然失效。
		sum := sha256.Sum256(fingerprint)
		version = hex.EncodeToString(sum[:4])
	})
}

func assetType(name string) string {
	if t, ok := assetTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

// Lookup 按名字取资源，只接受白名单里的名字。
func Lookup(name string) (*Asset, bool) {
	loadAssets()
	a, ok := assets[name]
	return a, ok
}

// AssetVersion 是所有静态资源的整体版本号，用于 ?v= 与缓存失效。
func AssetVersion() string {
	loadAssets()
	return version
}

// AssetNames 返回白名单，供测试与诊断使用。
func AssetNames() []string {
	loadAssets()
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	return names
}

// Warm 在后台预压缩大文件，避免第一个请求承担压缩延迟。
func Warm() {
	loadAssets()
	for _, a := range assets {
		if len(a.Data) > 64<<10 {
			go a.Gzip()
		}
	}
}
