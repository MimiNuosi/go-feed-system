package grafanaconfig

import (
	"encoding/json"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGrafanaProvisioningFilesParse(t *testing.T) {
	yamlFiles := []string{
		"provisioning/datasources/prometheus.yml",
		"provisioning/dashboards/dashboards.yml",
	}

	for _, file := range yamlFiles {
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

func TestGrafanaDashboardJSONParses(t *testing.T) {
	content, err := os.ReadFile("dashboards/go-feed-system.json")
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}

	var dashboard struct {
		Title  string `json:"title"`
		UID    string `json:"uid"`
		Panels []any  `json:"panels"`
	}
	if err := json.Unmarshal(content, &dashboard); err != nil {
		t.Fatalf("parse dashboard: %v", err)
	}
	if dashboard.Title != "Go Feed System" {
		t.Fatalf("unexpected dashboard title %q", dashboard.Title)
	}
	if dashboard.UID != "go-feed-system" {
		t.Fatalf("unexpected dashboard UID %q", dashboard.UID)
	}
	if len(dashboard.Panels) != 8 {
		t.Fatalf("expected 8 panels, got %d", len(dashboard.Panels))
	}
}
