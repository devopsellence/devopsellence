package desiredstate

import "testing"

func TestContainerName(t *testing.T) {
	name, err := ContainerName("Web", "rev-1", "abcdef123456")
	if err != nil {
		t.Fatalf("name error: %v", err)
	}
	if name != "svc-web-rev-1-abcdef12" {
		t.Fatalf("unexpected name: %s", name)
	}
}

func TestContainerNameSanitizeEmpty(t *testing.T) {
	if _, err := ContainerName("!!!", "rev-1", "abc"); err == nil {
		t.Fatal("expected error")
	}
}
