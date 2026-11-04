// Package web 内嵌前端资源：模板、样式、脚本与 mermaid。
package web

import "embed"

//go:embed *.html
//go:embed vendor
var FS embed.FS
