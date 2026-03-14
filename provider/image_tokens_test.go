package provider

import "testing"

func TestEstimateImageTokensFromDimensions(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		want          int
	}{
		{"small 512x512", 512, 512, (512 * 512) / 750},
		{"1024x768 no scaling", 1024, 768, (1024 * 768) / 750},
		{"1024x1024 no scaling", 1024, 1024, (1024 * 1024) / 750},
		{"1920x1080 scaled", 1920, 1080, func() int {
			// scale: 1568/1920 = 0.8167, new: 1568x882
			w := int(float64(1920) * 1568.0 / 1920.0)
			h := int(float64(1080) * 1568.0 / 1920.0)
			return (w * h) / 750
		}()},
		{"4096x2160 scaled", 4096, 2160, func() int {
			scale := 1568.0 / 4096.0
			w := int(4096.0 * scale)
			h := int(2160.0 * scale)
			return (w * h) / 750
		}()},
		{"zero width", 0, 100, imageTokensFallback},
		{"zero height", 100, 0, imageTokensFallback},
		{"tiny 10x10", 10, 10, 1}, // (100/750)=0 → clamped to 1
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateImageTokensFromDimensions(tt.width, tt.height)
			if got != tt.want {
				t.Errorf("estimateImageTokensFromDimensions(%d, %d) = %d, want %d", tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestEstimateImageTokens_FileNotFound(t *testing.T) {
	got := EstimateImageTokens("/nonexistent/path/image.jpg")
	if got != imageTokensFallback {
		t.Errorf("EstimateImageTokens(nonexistent) = %d, want %d", got, imageTokensFallback)
	}
}
