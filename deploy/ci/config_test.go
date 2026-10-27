package ciconfig

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComposeDefinesTwoAPIsBehindNginx(t *testing.T) {
	content, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			Ports  []string `yaml:"ports"`
			Expose []string `yaml:"expose"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &compose); err != nil {
		t.Fatalf("parse compose: %v", err)
	}

	for _, service := range []string{"api-1", "api-2"} {
		value, ok := compose.Services[service]
		if !ok {
			t.Fatalf("compose is missing %s", service)
		}
		if len(value.Ports) != 0 {
			t.Fatalf("%s must not publish a host port: %v", service, value.Ports)
		}
		if len(value.Expose) == 0 || value.Expose[0] != "8080" {
			t.Fatalf("%s expose = %v, want 8080", service, value.Expose)
		}
	}
	if _, ok := compose.Services["api"]; ok {
		t.Fatal("single api service should not remain")
	}
	if len(compose.Services["nginx"].Ports) == 0 {
		t.Fatal("nginx must publish the public API port")
	}
}

func TestGitHubActionsWorkflowParses(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	var workflow struct {
		Name string         `yaml:"name"`
		Jobs map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	if workflow.Name != "Go CI" {
		t.Fatalf("unexpected workflow name %q", workflow.Name)
	}
	if _, ok := workflow.Jobs["test"]; !ok {
		t.Fatal("expected test job")
	}
}
