package chat

import (
	"context"
	"testing"
)

func TestHandleModels_PublishesModalitiesForVisionModels(t *testing.T) {
	h := &ChatHandler{}
	models := h.buildModelsList(context.Background())
	if len(models) == 0 {
		t.Fatal("expected models to be returned")
	}

	foundVision := false
	for _, m := range models {
		hasImage := false
		for _, mod := range m.InputModalities {
			if mod == "image" {
				hasImage = true
				break
			}
		}
		if hasImage {
			foundVision = true
			if len(m.OutputModalities) == 0 || m.OutputModalities[0] != "text" {
				t.Fatalf("expected model %s output_modalities to contain text, got %v", m.ID, m.OutputModalities)
			}
			break
		}
	}

	if !foundVision {
		t.Fatal("expected to find at least one vision model with input_modalities containing 'image'")
	}
}
