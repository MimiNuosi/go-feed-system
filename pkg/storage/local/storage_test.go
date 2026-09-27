package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-feed-system/pkg/storage"
)

// 辅助函数：断言文件是否存在
func assertFileExists(t *testing.T, path string, shouldExist bool) {
	t.Helper() // 告诉测试框架这是辅助函数，报错时跳过此栈帧
	_, err := os.Stat(path)
	exists := !os.IsNotExist(err)
	if exists != shouldExist {
		t.Fatalf("file existence mismatch for %s: expected %v, got %v", path, shouldExist, exists)
	}
}

func TestLocalStorage_SaveAndOpen(t *testing.T) {
	tmpDir := t.TempDir() // 创建测试专用的临时目录
	store, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	ctx := context.Background()
	key := "videos/2026/09/test-video.mp4"
	content := "hello, this is a fake video content"
	reader := strings.NewReader(content)

	// 1. 测试 Save 成功
	err = store.Save(ctx, key, reader, int64(len(content)), "video/mp4")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 验证物理文件是否真的生成
	expectedPath := filepath.Join(tmpDir, key)
	assertFileExists(t, expectedPath, true)

	// 2. 测试 Open 成功并读取内容
	rc, err := store.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("content mismatch: expected %q, got %q", content, string(data))
	}

	// 3. 测试 Save 覆盖冲突 (O_EXCL 验证)
	err = store.Save(ctx, key, strings.NewReader("new content"), 11, "video/mp4")
	if err == nil {
		t.Error("expected error when saving with existing key, got nil")
	}

	// 4. 测试 Delete 成功
	err = store.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	assertFileExists(t, expectedPath, false)

	// 5. 测试 Delete 幂等性 (删除不存在的文件不报错)
	err = store.Delete(ctx, key)
	if err != nil {
		t.Errorf("Delete should be idempotent, got error: %v", err)
	}

	// 6. 测试 Open 不存在的文件
	_, err = store.Open(ctx, "videos/9999/10/nonexistent.mp4")
	if !errors.Is(err, storage.ErrObjectNotFound) {
		t.Errorf("expected ErrObjectNotFound, got: %v", err)
	}
}

// 测试路径穿越防御
func TestLocalStorage_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStorage(tmpDir)
	ctx := context.Background()

	// 恶意路径集合
	maliciousKeys := []string{
		"../../../etc/passwd",
		"..",
		"videos/../../../root/.ssh/id_rsa",
		filepath.Join("..", "..", "outside"),
	}

	for _, key := range maliciousKeys {
		t.Run(key, func(t *testing.T) {
			// 测试 Save 被拦截
			err := store.Save(ctx, key, strings.NewReader("attack"), 6, "video/mp4")
			if !errors.Is(err, storage.ErrInvalidInput) {
				t.Errorf("Save: expected ErrInvalidInput, got: %v", err)
			}

			// 测试 Open 被拦截
			_, err = store.Open(ctx, key)
			if !errors.Is(err, storage.ErrInvalidInput) {
				t.Errorf("Open: expected ErrInvalidInput, got: %v", err)
			}

			// 测试 Delete 被拦截
			err = store.Delete(ctx, key)
			if !errors.Is(err, storage.ErrInvalidInput) {
				t.Errorf("Delete: expected ErrInvalidInput, got: %v", err)
			}
		})
	}
}

// 测试 NewStorage 时目录创建逻辑
func TestNewStorage_CreateDir(t *testing.T) {
	// 创建一个不存在的嵌套目录路径
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "nested", "data", "videos")

	_, err := NewStorage(baseDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// 验证目录是否被成功创建
	info, err := os.Stat(baseDir)
	if err != nil {
		t.Fatalf("baseDir was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("baseDir is not a directory")
	}
}
