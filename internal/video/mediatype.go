package video

import "encoding/binary"

// MaxSniffBytes 是判定容器类型需要读取的文件头长度。
//
// ftyp box 通常只有几十字节，512 字节足够覆盖它以及 EBML 头。
const MaxSniffBytes = 512

// Container 是服务端从文件内容里嗅探出来的媒体容器。
//
// MIME 用于入库，Extension 用于生成 StorageKey。
// 两者都来自文件字节本身，绝不采用客户端声明的值。
type Container struct {
	MIME      string
	Extension string
}

var (
	containerMP4  = Container{MIME: "video/mp4", Extension: ".mp4"}
	containerMOV  = Container{MIME: "video/quicktime", Extension: ".mov"}
	containerWEBM = Container{MIME: "video/webm", Extension: ".webm"}
	container3GP  = Container{MIME: "video/3gpp", Extension: ".3gp"}
)

// isoBMFFBrands 把 ISO-BMFF 的 ftyp box 里的 brand 映射到容器。
//
// .m4v/.m4a 与 .mp4 是同一个容器，这里统一归并为 video/mp4，
// 避免同一份数据出现多个 MIME。以后要支持新格式，只需要往这张表里加 brand。
var isoBMFFBrands = map[string]Container{
	"isom": containerMP4,
	"iso2": containerMP4,
	"iso4": containerMP4,
	"iso5": containerMP4,
	"iso6": containerMP4,
	"mp41": containerMP4,
	"mp42": containerMP4,
	"avc1": containerMP4,
	"M4V ": containerMP4,
	"M4A ": containerMP4,
	"dash": containerMP4,
	"msdh": containerMP4,
	"qt  ": containerMOV,
	"3gp4": container3GP,
	"3gp5": container3GP,
	"3g2a": container3GP,
}

// SniffContainer 只看文件头字节判断容器类型，不信任任何客户端声明。
//
// 这里刻意不使用 http.DetectContentType：它的契约是"浏览器该怎么渲染这段
// 字节"，而且只在一串 brand 里搜到字面量 "mp4" 才返回 video/mp4。结果是
// ffmpeg -brand avc1 生成的真 mp4、QuickTime、3gp 全都被判成未知类型。
func SniffContainer(header []byte) (Container, bool) {
	if container, ok := sniffEBML(header); ok {
		return container, true
	}
	return sniffISOBaseMediaFile(header)
}

// sniffEBML 识别 EBML 头（WebM 和 MKV 共用这一族容器）。
func sniffEBML(header []byte) (Container, bool) {
	if len(header) < 4 {
		return Container{}, false
	}
	if header[0] == 0x1A && header[1] == 0x45 &&
		header[2] == 0xDF && header[3] == 0xA3 {
		return containerWEBM, true
	}
	return Container{}, false
}

// sniffISOBaseMediaFile 解析 ISO-BMFF 的 ftyp box。
//
// 大端布局：
//
//	offset 0   4 字节  box 总长度
//	offset 4   4 字节  固定为 "ftyp"
//	offset 8   4 字节  major_brand
//	offset 12  4 字节  minor_version（版本号，不是 brand）
//	offset 16  起      每 4 字节一个 compatible brand
//
// brand 只存在于文件头里，客户端想伪造就得真的写出一个合法的 ftyp box。
func sniffISOBaseMediaFile(header []byte) (Container, bool) {
	const ftypHeaderSize = 16 // box 长度 + "ftyp" + major_brand + minor_version
	if len(header) < ftypHeaderSize {
		return Container{}, false
	}

	boxSize := int(binary.BigEndian.Uint32(header[0:4]))
	if boxSize < ftypHeaderSize || boxSize%4 != 0 {
		return Container{}, false
	}
	if string(header[4:8]) != "ftyp" {
		return Container{}, false
	}

	// 极端情况下 box 比我们读到的头部还长，此时只扫描已经拿到的部分。
	limit := boxSize
	if limit > len(header) {
		limit = len(header)
	}
	for offset := 8; offset+4 <= limit; offset += 4 {
		if offset == 12 {
			continue // minor_version 不是 brand
		}
		if container, ok := isoBMFFBrands[string(header[offset:offset+4])]; ok {
			return container, true
		}
	}
	return Container{}, false
}
