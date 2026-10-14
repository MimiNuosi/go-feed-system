// Package web 内嵌最小前端页面。
//
// 用 go:embed 而不是读磁盘文件：镜像里不需要额外挂载目录，
// Dockerfile 也不用改（构建阶段本来就 COPY 了整个仓库），
// 单文件二进制拿起来就能跑。
//
// 页面只依赖浏览器原生能力（HTML + CSS + fetch），不需要 npm 和构建链；
// 它是用来做端到端人工验证的控制台，不是产品前端。
package web

import _ "embed"

//go:embed index.html
var indexHTML []byte

// IndexHTML 返回首页内容。
//
// 返回副本，避免调用方改到内嵌的原始字节。
func IndexHTML() []byte {
	out := make([]byte, len(indexHTML))
	copy(out, indexHTML)
	return out
}
