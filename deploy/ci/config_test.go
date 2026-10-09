package ciconfig

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

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
