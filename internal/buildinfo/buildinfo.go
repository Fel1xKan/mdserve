// Package buildinfo 保存编译期注入的版本信息。
package buildinfo

import "runtime"

// 由 goreleaser / Makefile 通过 -ldflags 注入：
//
//	-X github.com/Fel1xKan/mdserve/internal/buildinfo.Version=1.2.3
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// IsDev 表示这是本地构建，没有可替换的版本身份。
func IsDev() bool { return Version == "dev" }

// Summary 形如 "1.2.3 (abc1234, 2026-11-04T00:00:00Z)"。
func Summary() string {
	if IsDev() {
		return "dev"
	}
	return Version + " (" + Commit + ", " + Date + ")"
}

// Platform 返回 "goos/goarch"，供 version --json 与升级脚本使用。
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
