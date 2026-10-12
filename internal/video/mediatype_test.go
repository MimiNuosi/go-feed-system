package video

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildFtyp 构造一个真实结构的 ftyp box，用来验证嗅探器。
func buildFtyp(major string, compatible ...string) []byte {
	var brands bytes.Buffer
	brands.WriteString(major)
	brands.Write([]byte{0x00, 0x00, 0x02, 0x00}) // minor_version
	for _, brand := range compatible {
		brands.WriteString(brand)
	}

	size := 8 + brands.Len()
	header := make([]byte, 0, size)
	header = binary.BigEndian.AppendUint32(header, uint32(size))
	header = append(header, "ftyp"...)
	header = append(header, brands.Bytes()...)
	return header
}

func TestSniffContainer(t *testing.T) {
	webmHeader := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte("rest of ebml")...)

	oddBox := buildFtyp("isom", "isom")
	oddBox[3] = 0x15 // box 长度 20 -> 21，不是 4 的倍数

	tooSmallBox := []byte{0x00, 0x00, 0x00, 0x08}
	tooSmallBox = append(tooSmallBox, "ftyp"...)
	tooSmallBox = append(tooSmallBox, make([]byte, 8)...)

	tests := []struct {
		name   string
		header []byte
		want   Container
		wantOK bool
	}{
		{
			name:   "mp4 isom/iso2/avc1/mp41",
			header: buildFtyp("isom", "isom", "iso2", "avc1", "mp41"),
			want:   containerMP4,
			wantOK: true,
		},
		{
			name:   "mp4 ffmpeg -brand avc1",
			header: buildFtyp("avc1", "avc1", "isom"),
			want:   containerMP4,
			wantOK: true,
		},
		{
			name:   "brand 出现在 compatible 列表里",
			header: buildFtyp("xxxx", "isom"),
			want:   containerMP4,
			wantOK: true,
		},
		{
			name:   "m4v 归并为 mp4",
			header: buildFtyp("M4V ", "M4V ", "M4A ", "isom"),
			want:   containerMP4,
			wantOK: true,
		},
		{
			name:   "mov qt",
			header: buildFtyp("qt  ", "qt  "),
			want:   containerMOV,
			wantOK: true,
		},
		{
			name:   "3gp",
			header: buildFtyp("3gp4", "3gp4", "isom"),
			want:   container3GP,
			wantOK: true,
		},
		{
			name:   "mp4 头部后面跟着真实数据",
			header: append(buildFtyp("isom", "mp41"), []byte("moov data")...),
			want:   containerMP4,
			wantOK: true,
		},
		{
			name:   "webm EBML",
			header: webmHeader,
			want:   containerWEBM,
			wantOK: true,
		},
		{name: "空头部", header: nil, wantOK: false},
		{name: "头部不足 16 字节", header: buildFtyp("isom")[:12], wantOK: false},
		{
			name:   "box 长度不是 4 的倍数",
			header: oddBox,
			wantOK: false,
		},
		{
			name:   "box 长度过小",
			header: tooSmallBox,
			wantOK: false,
		},
		{
			name:   "不是 ftyp 开头",
			header: append([]byte{0x00, 0x00, 0x00, 0x20}, []byte("moov....")...),
			wantOK: false,
		},
		{
			name:   "brand 不在白名单",
			header: buildFtyp("zzzz"),
			wantOK: false,
		},
		{
			name:   "文本文件改名",
			header: []byte("this is definitely not a video"),
			wantOK: false,
		},
		{
			name:   "zip 改名",
			header: append([]byte("PK\x03\x04"), make([]byte, 60)...),
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SniffContainer(tt.header)
			if ok != tt.wantOK {
				t.Fatalf("SniffContainer ok = %v, want %v (header=%q)", ok, tt.wantOK, tt.header)
			}
			if got != tt.want {
				t.Fatalf("SniffContainer = %+v, want %+v", got, tt.want)
			}
		})
	}
}
