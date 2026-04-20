package guidebook

import "testing"

func TestReadSkill(t *testing.T) {
	b, err := Read("skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 200 {
		t.Fatalf("skill content too short: %d", len(b))
	}
}

func TestTopicNames(t *testing.T) {
	names, err := TopicNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 || names[0] != DefaultTopic {
		t.Fatalf("unexpected names: %q", names)
	}
}
