package prometheusconfig

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPrometheusConfigFilesParse(t *testing.T) {
	files := []string{
		"prometheus.yml",
		"alerts.yml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}

			var parsed any
			if err := yaml.Unmarshal(content, &parsed); err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
		})
	}
}
