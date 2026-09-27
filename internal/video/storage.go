package video

import (
	"context"
	"io"
)

// ObjectStorage 定义视频文件存储能力。
//
// 它只处理二进制流，不依赖 multipart.FileHeader 或 Gin。
// 本地磁盘和 MinIO 都应实现这组方法。

type ObjectStorage interface {
	Save(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
}
