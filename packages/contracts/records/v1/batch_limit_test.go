package recordsv1

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOutboundMessageTurnLimitMatchesGoContract(t *testing.T) {
	specData, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					MaxItems int `yaml:"maxItems"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("parse OpenAPI spec: %v", err)
	}

	got := spec.Components.Schemas["CreateMessageRequest"].Properties["turn_ids"].MaxItems
	if got != MaxFinalTurnBatchSize {
		t.Fatalf("CreateMessageRequest.turn_ids maxItems = %d, want %d", got, MaxFinalTurnBatchSize)
	}
}
