package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go-feed-system/pkg/storage"
)

// Storage 实现 ObjectStorage 接口的本地磁盘版本
type Storage struct {
	baseDir string // 视频存放的根目录，例如 "./data/videos"
}

// NewStorage 创建本地存储实例
func NewStorage(baseDir string) (*Storage, error) {
	// 在启动时确保根目录存在
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	return &Storage{baseDir: baseDir}, nil
}

// 公共校验函数
func (s *Storage) validateAndGetPath(key string) (string, error) {
	absBase, err := filepath.Abs(s.baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir: %w", err)
	}

	fullPath := filepath.Join(absBase, key)
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve full path: %w", err)
	}

	// 校验最终路径是否在 baseDir 内
	absBaseWithSep := absBase + string(os.PathSeparator)
	if !strings.HasPrefix(absFull, absBaseWithSep) {
		return "", fmt.Errorf("invalid storage key: %w", storage.ErrInvalidInput)
	}
	return absFull, nil
}

func (s *Storage) Save(ctx context.Context, key string, reader io.Reader, size int64, contentType string) (err error) {
	// 1. 核心：调用公共校验函数
	absFull, err := s.validateAndGetPath(key)
	if err != nil {
		return err
	}

	// 2. 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(absFull), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	// 3. 创建文件（使用 O_EXCL 防止覆盖）
	dst, err := os.OpenFile(absFull, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		if closeErr := dst.Close(); closeErr != nil {
			err = fmt.Errorf("close file: %w", closeErr)
		}
	}()

	// 4. 拷贝流
	written, err := io.Copy(dst, reader)
	if err != nil {
		_ = os.Remove(absFull)
		return fmt.Errorf("copy file: %w", err)
	}

	if written != size {
		_ = os.Remove(absFull)
		return fmt.Errorf("incomplete upload: expected %d bytes, wrote %d bytes", size, written)
	}

	// 通过检查 ctx 来提前取消大文件写入
	select {
	case <-ctx.Done():
		_ = os.Remove(absFull)
		return ctx.Err()
	default:
	}

	return nil
}

func (s *Storage) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	// 核心：使用校验函数
	absFull, err := s.validateAndGetPath(key)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(absFull)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrObjectNotFound
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	return file, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	// 核心：使用校验函数
	absFull, err := s.validateAndGetPath(key)
	if err != nil {
		return err
	}

	if err := os.Remove(absFull); err != nil {
		if os.IsNotExist(err) {
			return nil // 幂等
		}
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}
