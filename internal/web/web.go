// Package web 内嵌前端页面。
//
// 用 go:embed 而不是读磁盘文件：镜像里不需要额外挂载目录，
// Dockerfile 也不用改（构建阶段本来就 COPY 了整个仓库），
// 单文件二进制拿起来就能跑。
//
// 这里有两套页面：
//   - dist/：Vue 3 + Vite 构建出来的产品前端，挂在 /；
//   - index.html：早期不依赖 npm 的单文件控制台，保留在 /legacy 作为兜底。
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

//go:embed index.html
var legacyHTML []byte

// Dist 返回构建好的前端静态文件树（internal/web/dist）。
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// embed 的内容在编译期固定，这里不可能失败。
		panic("web: embedded dist is invalid: " + err.Error())
	}
	return sub
}

// LegacyHTML 返回旧版单文件控制台。
//
// 返回副本，避免调用方改到内嵌的原始字节。
func LegacyHTML() []byte {
	out := make([]byte, len(legacyHTML))
	copy(out, legacyHTML)
	return out
}
