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

func TestPrometheusScrapesBothAPIs(t *testing.T) {
	content, err := os.ReadFile("prometheus.yml")
	if err != nil {
		t.Fatalf("read prometheus.yml: %v", err)
	}
	var config struct {
		ScrapeConfigs []struct {
			StaticConfigs []struct {
				Targets []string `yaml:"targets"`
			} `yaml:"static_configs"`
		} `yaml:"scrape_configs"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		t.Fatalf("parse prometheus.yml: %v", err)
	}
	if len(config.ScrapeConfigs) == 0 || len(config.ScrapeConfigs[0].StaticConfigs) == 0 {
		t.Fatal("missing API scrape targets")
	}
	targets := config.ScrapeConfigs[0].StaticConfigs[0].Targets
	if len(targets) != 2 || targets[0] != "api-1:8080" || targets[1] != "api-2:8080" {
		t.Fatalf("unexpected API targets: %v", targets)
	}
}
