package templates

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBeadsAgentSkill_NonEmpty(t *testing.T) {
	got := BeadsAgentSkill()
	if strings.TrimSpace(got) == "" {
		t.Fatal("BeadsAgentSkill() is empty; the embedded SKILL.md failed to embed")
	}
	// SKILL.md is a front-matter markdown file: it must open with the YAML
	// front-matter fence and name the skill.
	if !strings.HasPrefix(got, "---") {
		t.Errorf("SKILL.md does not start with front-matter fence; got prefix %q", firstLine(got))
	}
	if !strings.Contains(got, "name: beads") {
		t.Error("SKILL.md front-matter is missing 'name: beads'")
	}
}

func TestBeadsAgentSkillOpenAIYAML_WellFormed(t *testing.T) {
	got := BeadsAgentSkillOpenAIYAML()
	if strings.TrimSpace(got) == "" {
		t.Fatal("BeadsAgentSkillOpenAIYAML() is empty; the embedded openai.yaml failed to embed")
	}
	// It must be parseable YAML exposing the interface metadata bd relies on.
	var doc struct {
		Interface struct {
			DisplayName      string `yaml:"display_name"`
			ShortDescription string `yaml:"short_description"`
		} `yaml:"interface"`
	}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("openai.yaml is not valid YAML: %v", err)
	}
	if doc.Interface.DisplayName == "" {
		t.Error("openai.yaml interface.display_name is empty")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
