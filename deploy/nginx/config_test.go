package nginxconfig

import (
	"os"
	"strings"
	"testing"
)

func TestNginxConfigKeepsSSEAndUploadsWorking(t *testing.T) {
	content, err := os.ReadFile("nginx.conf")
	if err != nil {
		t.Fatalf("read nginx.conf: %v", err)
	}
	config := string(content)
	required := []string{
		"server api-1:8080",
		"server api-2:8080",
		"proxy_buffering off;",
		"proxy_request_buffering off;",
		"proxy_read_timeout 1h;",
		"client_max_body_size 512m;",
		"X-Forwarded-For $proxy_add_x_forwarded_for;",
	}
	for _, fragment := range required {
		t.Run(fragment, func(t *testing.T) {
			if !strings.Contains(config, fragment) {
				t.Fatalf("nginx.conf is missing %q", fragment)
			}
		})
	}
}
