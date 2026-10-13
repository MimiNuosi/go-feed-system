package loadtestconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// targetLine 匹配 vegeta 目标文件里的一行请求定义。
var targetLine = regexp.MustCompile(`^(GET|POST|PUT|DELETE) https?://\S+$`)

func TestTargetFilesAreValid(t *testing.T) {
	files := []string{
		"livez.targets",
		"hot.targets",
		"video_detail.targets",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}

			requests := 0
			for _, line := range strings.Split(string(content), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if !targetLine.MatchString(line) {
					t.Fatalf("%s: 非法目标行 %q", file, line)
				}
				requests++
			}

			if requests == 0 {
				t.Fatalf("%s: 至少要有一条请求定义", file)
			}
		})
	}
}

// TestScriptsExistAndDocumentPrerequisites 保证脚本还在，
// 并且仍然写着"压测前要放开限流"这个前提 —— 少了它，
// 压出来的数字会全部被 429 截断，结论就是错的。
func TestScriptsExistAndDocumentPrerequisites(t *testing.T) {
	tests := []struct {
		script   string
		required []string
	}{
		{
			script:   "run.ps1",
			required: []string{"vegeta", "RATE_LIMIT_ENABLED"},
		},
		{
			script:   "sample.ps1",
			required: []string{"vegeta", "MYSQL_PASSWORD"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			content, err := os.ReadFile(tt.script)
			if err != nil {
				t.Fatalf("read %s: %v", tt.script, err)
			}

			text := string(content)
			for _, want := range tt.required {
				if !strings.Contains(text, want) {
					t.Errorf("%s 里应该提到 %q", tt.script, want)
				}
			}
		})
	}
}

// TestReadmeDocumentsAllTargets 保证 README 和目标文件不会走散。
func TestReadmeDocumentsAllTargets(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	targets, err := filepath.Glob("*.targets")
	if err != nil {
		t.Fatalf("glob targets: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("没有找到任何 .targets 文件")
	}

	for _, target := range targets {
		name := strings.TrimSuffix(target, ".targets")
		if !strings.Contains(string(readme), name) {
			t.Errorf("README.md 里没有提到压测目标 %q", name)
		}
	}
}
